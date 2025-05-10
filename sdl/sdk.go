package sample

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type TunnelConfig struct {
	TunnelServer string
	AuthToken    string
	LocalPort    string

	OnAuth           func(token string)
	OnConnected      func(localUrl, prodUrl, tunnelId string)
	OnDisconnected   func()
	OnError          func(err error)
	OnRequest        func(msg TunnelMessage)
	OnSedingResponse func(msg TunnelMessage, resp *http.Response, body []byte)
	Logger           *log.Logger
}

type TunnelClient struct {
	// used to testing the tunnel from local server
	localURL string

	prodURL  string
	tunnelID string

	config *TunnelConfig
	conn   net.Conn
	status TunnelStatus

	// this used to print an error either from the tunnel or local server
	errorCh chan error
}

var DefaultTunnelConfig = TunnelConfig{
	TunnelServer: "tunnel.ngorok.site:9000",
	Logger:       slog.NewLogLogger(slog.NewTextHandler(os.Stdout, nil), slog.LevelInfo),
}

func NewTunnelClient(config *TunnelConfig, port string) (*TunnelClient, error) {
	if config == nil {
		config = &DefaultTunnelConfig
	}

	if config.OnConnected == nil {
		config.OnConnected = func(localUrl, prodUrl, tunnelId string) {
			config.Logger.Printf("Tunnel established! ID: %s", tunnelId)
			config.Logger.Printf("Local URL: %s, Production URL: %s", localUrl, prodUrl)
			config.Logger.Printf("Forwarding traffic from http://localhost:%s", config.LocalPort)
		}
	}

	if config.OnDisconnected == nil {
		config.OnDisconnected = func() {
			config.Logger.Println("Tunnel disconnected")
		}
	}

	if config.OnError == nil {
		config.OnError = func(err error) {
			config.Logger.Println("Error", err)
		}
	}

	if config.OnRequest == nil {
		config.OnRequest = func(msg TunnelMessage) {
			config.Logger.Printf("Received request [%s] %s %s", msg.ID, msg.Method, msg.Path)
		}
	}

	if config.OnSedingResponse == nil {
		config.OnSedingResponse = func(msg TunnelMessage, resp *http.Response, body []byte) {
			config.Logger.Printf("Sending response [%s] %d %s [%d bytes]", msg.ID, resp.StatusCode, msg.Path, len(body))

		}
	}

	if config.OnAuth == nil {
		config.OnAuth = func(token string) {
			config.Logger.Println("Authenticated with token", token)
		}
	}

	config.LocalPort = port

	return &TunnelClient{
		config: config,
		status: StatusDisconnected,
	}, nil
}

// Establish a tunnel connection with the server, including authentication
func (c *TunnelClient) Connect(token string) error {
	c.status = StatusConnecting
	c.config.AuthToken = token
	c.config.OnAuth(token)

	conn, err := net.Dial("tcp", c.config.TunnelServer)
	if err != nil {
		c.status = StatusError
		c.config.OnError(err)
		return err
	}

	c.conn = conn

	c.status = StatusAuthenticating
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	tunnelMessage := TunnelMessage{
		Type: TunnelAuthRequest,
		Body: token,
	}

	if err := encoder.Encode(tunnelMessage); err != nil {
		c.status = StatusError
		c.config.OnError(err)
		conn.Close()

		return err
	}

	// set deadline for authentication
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := decoder.Decode(&tunnelMessage); err != nil {
		c.status = StatusError
		c.config.OnError(err)
		conn.Close()

		return err
	}

	// unset deadline
	conn.SetReadDeadline(time.Time{})

	if tunnelMessage.Type == TunnelAuthFailure {
		c.status = StatusError
		c.config.OnError(err)
		conn.Close()

		return err
	}

	c.status = StatusEstablishing

	if tunnelMessage.Type != TunnelCreated {
		c.status = StatusError
		c.config.OnError(err)
		conn.Close()

		return fmt.Errorf("expected tunnel created message, got %d", tunnelMessage.Type)
	}

	c.localURL = tunnelMessage.Headers[HeaderLocalUrl]
	c.prodURL = tunnelMessage.Headers[HeaderProdUrl]
	c.tunnelID = tunnelMessage.ID

	c.status = StatusConnected
	c.config.OnConnected(c.localURL, c.prodURL, c.tunnelID)

	return nil
}

func (c *TunnelClient) Start(token string) error {
	if err := c.Connect(token); err != nil {
		return err
	}

	c.handleTunnelRequests()

	// TODO: handle the local test server later

	return nil
}

func (c *TunnelClient) handleTunnelRequests() {
	decoder := json.NewDecoder(c.conn)

	var msg TunnelMessage
	for {
		select {
		case <-c.errorCh:
			return
		default:
			if err := decoder.Decode(&msg); err != nil {
				if err == io.EOF || strings.Contains(err.Error(), "use of closed network connection") {
					err = errors.New("COnnection closed")
					c.config.OnError(err)
					c.errorCh <- err
				} else {
					c.config.OnError(errors.New("Error while decoding the message: " + err.Error()))
				}

				c.status = StatusDisconnected
				return
			}

			if msg.Type == TunnelRequest {
				go c.handleLocalRequests(msg)
			} else {
				c.config.OnError(fmt.Errorf("Unexpected message type: %d", msg.Type))
			}
		}
	}
}

func (c *TunnelClient) Stop(msg TunnelMessage) error {
	if c.status == StatusDisconnected {
		return nil
	}

	close(c.errorCh)

	if c.conn != nil {
		c.conn.Close()
	}

	c.status = StatusDisconnected
	c.config.OnDisconnected()
	return nil
}

func (c *TunnelClient) handleLocalRequests(msg TunnelMessage) {
	c.config.OnRequest(msg)

	// local target url
	targetURL := fmt.Sprintf("http://localhost:%s%s", c.config.LocalPort, msg.Path)
	req, err := http.NewRequest(msg.Method, targetURL, strings.NewReader(msg.Body))
	if err != nil {
		c.config.OnError(errors.New("Error creating request: " + err.Error()))
		c.sendErrorResponse(msg.ID, http.StatusInternalServerError, "Error creating request: "+err.Error())
		return
	}

	for key, value := range msg.Headers {
		if strings.EqualFold(key, "Host") {
			continue
		}

		if strings.EqualFold(key, "X-Forwarded-Host") {
			req.Host = value

			// continue
		}

		req.Header.Set(key, value)
	}

	if req.Host == "" {
		req.Host = "localhost:" + c.config.LocalPort
	}

	client := &http.Client{
		Timeout: 20 * time.Second, // TODO: improve the timeout later
	}

	resp, err := client.Do(req)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			c.config.OnError(errors.New("Timeout connecting to the local service: " + err.Error()))
			c.sendErrorResponse(msg.ID, http.StatusGatewayTimeout, "Local service timed out")
		} else {
			c.config.OnError(errors.New("Error connecting to the local service: " + err.Error()))
			c.sendErrorResponse(msg.ID, http.StatusBadGateway, "Error connecting to the local service: "+err.Error())
		}

		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.config.OnError(errors.New("Error reading the response body: " + err.Error()))
		c.sendErrorResponse(msg.ID, http.StatusInternalServerError, "Failed to read local response body")

		return
	}

	defer resp.Body.Close()

	c.config.OnSedingResponse(msg, resp, body)

	responseHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			responseHeaders[key] = values[0]
		}
	}

	responseHeaders["X-Status-Code"] = strconv.Itoa(resp.StatusCode)
	msg = TunnelMessage{ // response the server
		Type:    TunnelResponse,
		ID:      msg.ID,
		Headers: responseHeaders,
		Body:    string(body),
	}

	encoder := json.NewEncoder(c.conn)
	if err := encoder.Encode(msg); err != nil {
		c.config.OnError(errors.New("Error sending response: " + err.Error()))
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
		c.config.OnError(errors.New("Error sending error oresponse: " + err.Error()))
	}
}
