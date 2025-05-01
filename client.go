package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client interface {
	Start() error
	Stop() error
	Status() TunnelStatus
	URLs() (localURL string, productionURL string)
	TunnelID() string
}

type TunnelClient struct {
	config        *Config
	conn          net.Conn
	status        TunnelStatus
	statusMu      sync.RWMutex
	localURL      string
	productionURL string
	tunnelID      string
	stopCh        chan struct{}
	logger        *log.Logger
}

func NewClient(config *Config, tunnelServer string) (Client, error) {
	if config == nil {
		if tunnelServer == "" {
			return nil, errors.New("you need to set tunnel server")
		}

		config = DefaultConfig(tunnelServer)
	}

	if config.LocalPort == "" {
		return nil, ErrInvalidLocalPort
	}

	if _, err := strconv.Atoi(config.LocalPort); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLocalPort, err)
	}

	return &TunnelClient{
		config: config,
		status: StatusDisconnected,
		stopCh: make(chan struct{}),
		logger: log.New(io.Discard, "", log.LstdFlags),
	}, nil
}

func (c *TunnelClient) EnableLogging(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	c.logger = log.New(w, "[ngorok] ", log.LstdFlags)
}

func (c *TunnelClient) Start() error {
	c.setStatus(StatusConnecting)
	c.logger.Printf("Connecting to tunnel server at %s", c.config.TunnelServer)

	token, err := c.config.loadAuthToken()
	if err != nil {
		c.setStatus(StatusError)
		return err
	}

	conn, err := net.Dial("tcp", c.config.TunnelServer)
	if err != nil {
		c.setStatus(StatusError)
		return fmt.Errorf("failed to connect to tunnel server: %w", err)
	}
	c.conn = conn

	c.setStatus(StatusAuthenticating)
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	authReq := TunnelMessage{
		Type: TunnelAuthRequest,
		Body: token,
	}

	if err := encoder.Encode(authReq); err != nil {
		c.setStatus(StatusError)
		conn.Close()
		return fmt.Errorf("failed to send authentication request: %w", err)
	}

	var tunnelMsg TunnelMessage
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := decoder.Decode(&tunnelMsg); err != nil {
		c.setStatus(StatusError)
		conn.Close()
		return fmt.Errorf("failed to receive authentication response: %w", err)
	}
	conn.SetReadDeadline(time.Time{})

	if tunnelMsg.Type == TunnelAuthFailure {
		c.setStatus(StatusError)
		conn.Close()
		return fmt.Errorf("%w: %s", ErrAuthFailure, tunnelMsg.Body)
	}

	c.setStatus(StatusEstablishing)
	if tunnelMsg.Type != TunnelCreated {
		c.setStatus(StatusError)
		conn.Close()
		return fmt.Errorf("expected tunnel created message, got type %d", tunnelMsg.Type)
	}

	c.localURL = tunnelMsg.Headers["Local-URL"]
	c.productionURL = tunnelMsg.Headers["Prod-URL"]
	c.tunnelID = tunnelMsg.ID

	c.setStatus(StatusConnected)
	c.logger.Printf("Tunnel established! ID: %s", c.tunnelID)
	c.logger.Printf("Local URL: %s, Production URL: %s", c.localURL, c.productionURL)
	c.logger.Printf("Forwarding traffic to http://localhost:%s", c.config.LocalPort)

	if c.config.AuthToken != token {
		c.config.AuthToken = token
		if err := c.config.saveAuthToken(token); err != nil {
			c.logger.Printf("Warning: Failed to save auth token: %v", err)
		}
	}

	go c.handleTunnelRequests()

	return nil
}

func (c *TunnelClient) handleTunnelRequests() {
	decoder := json.NewDecoder(c.conn)
	for {
		select {
		case <-c.stopCh:
			return
		default:
			var msg TunnelMessage
			if err := decoder.Decode(&msg); err != nil {
				if err == io.EOF || strings.Contains(err.Error(), "use of closed network connection") {
					c.logger.Println("Connection closed")
				} else {
					c.logger.Printf("Error decoding message: %v", err)
				}

				c.setStatus(StatusDisconnected)
				return
			}

			if msg.Type == TunnelRequest {
				go c.handleLocalRequest(msg)
			} else {
				c.logger.Printf("Received unexpected message type: %d", msg.Type)
			}
		}
	}
}

func (c *TunnelClient) handleLocalRequest(msg TunnelMessage) {
	c.logger.Printf("Received request [%s] %s %s", msg.ID, msg.Method, msg.Path)

	targetURL := fmt.Sprintf("http://localhost:%s%s", c.config.LocalPort, msg.Path)
	req, err := http.NewRequest(msg.Method, targetURL, strings.NewReader(msg.Body))
	if err != nil {
		c.logger.Printf("Error creating request: %v", err)
		c.sendErrorResponse(msg.ID, http.StatusInternalServerError, "Failed to create local request")
		return
	}

	for key, value := range msg.Headers {
		if strings.EqualFold(key, "Host") {
			continue
		}

		if strings.EqualFold(key, "X-Forwarded-Host") {
			req.Host = value
		}
		req.Header.Set(key, value)
	}

	if req.Host == "" {
		req.Host = "localhost:" + c.config.LocalPort
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			c.logger.Printf("Timeout connecting to local service: %v", err)
			c.sendErrorResponse(msg.ID, http.StatusGatewayTimeout, "Local service timed out")
		} else {
			c.logger.Printf("Error connecting to local service: %v", err)
			c.sendErrorResponse(msg.ID, http.StatusBadGateway, "Failed to connect to local service")
		}
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Printf("Error reading response body: %v", err)
		c.sendErrorResponse(msg.ID, http.StatusInternalServerError, "Failed to read local response body")
		return
	}

	c.logger.Printf("Sending response [%s] %d %s [%d bytes]", msg.ID, resp.StatusCode, msg.Path, len(bodyBytes))

	responseHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			responseHeaders[key] = values[0]
		}
	}

	responseHeaders["X-Status-Code"] = strconv.Itoa(resp.StatusCode)

	responseMsg := TunnelMessage{
		Type:    TunnelResponse,
		ID:      msg.ID,
		Headers: responseHeaders,
		Body:    string(bodyBytes),
	}

	encoder := json.NewEncoder(c.conn)
	if err := encoder.Encode(responseMsg); err != nil {
		c.logger.Printf("Error sending response: %v", err)
	}
}

func (c *TunnelClient) sendErrorResponse(requestID string, statusCode int, message string) {
	if statusCode < 100 || statusCode > 599 {
		statusCode = http.StatusInternalServerError
	}

	responseMsg := TunnelMessage{
		Type: TunnelResponse,
		ID:   requestID,
		Headers: map[string]string{
			"X-Status-Code": strconv.Itoa(statusCode),
			"Content-Type":  "text/plain; charset=utf-8",
		},
		Body: fmt.Sprintf("%d %s: %s", statusCode, http.StatusText(statusCode), message),
	}

	encoder := json.NewEncoder(c.conn)
	if err := encoder.Encode(responseMsg); err != nil {
		c.logger.Printf("Error sending error response: %v", err)
	}
}

func (c *TunnelClient) Stop() error {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	if c.status == StatusDisconnected {
		return nil
	}

	close(c.stopCh)

	if c.conn != nil {
		c.conn.Close()
	}

	c.status = StatusDisconnected
	c.logger.Println("Tunnel stopped")
	return nil
}

func (c *TunnelClient) Status() TunnelStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *TunnelClient) URLs() (string, string) {
	return c.localURL, c.productionURL
}

func (c *TunnelClient) TunnelID() string {
	return c.tunnelID
}

func (c *TunnelClient) setStatus(status TunnelStatus) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.status = status
}
