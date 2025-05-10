package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TunnelConn struct {
	localURL string

	prodURL  string
	tunnelID string

	config    *TunnelConfig
	sdkConfig *SDKConfig

	conn   net.Conn
	status TunnelStatus

	errorCh chan error
}

func NewTunnelConn(config *TunnelConfig, sdkConfig *SDKConfig, port string) (*TunnelConn, error) {
	if config == nil {
		config = &DefaultTunnelConfig
	}

	if sdkConfig == nil {
		return nil, errors.New("SDK config is required")
	}

	config.LocalPort = port

	fmt.Println(config)

	return &TunnelConn{
		config:    config,
		sdkConfig: sdkConfig,
		status:    StatusDisconnected,
	}, nil
}

// Establish a tunnel connection with the server, including authentication
func (c *TunnelConn) Connect() error {
	c.status = StatusConnecting
	c.sdkConfig.OnAuth(c.sdkConfig.AuthToken)

	conn, err := net.Dial("tcp", c.sdkConfig.TunnelServer)
	if err != nil {
		c.status = StatusError
		c.sdkConfig.OnError(err)
		return err
	}

	c.conn = conn

	// start the authentication process
	c.status = StatusAuthenticating
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	tunnelMessage := TunnelMessage{
		Type: TunnelAuthRequest,
		Body: c.sdkConfig.AuthToken,
	}

	if err := encoder.Encode(tunnelMessage); err != nil {
		c.status = StatusError
		c.sdkConfig.OnError(err)
		conn.Close()

		return err
	}

	// set deadline for authentication
	conn.SetReadDeadline(time.Now().Add(c.config.AuthTimeout))
	if err := decoder.Decode(&tunnelMessage); err != nil {
		c.status = StatusError
		c.sdkConfig.OnError(err)
		conn.Close()

		return err
	}

	// unset deadline
	conn.SetReadDeadline(time.Time{})

	if tunnelMessage.Type == TunnelAuthFailure {
		c.status = StatusError
		c.sdkConfig.OnError(err)
		conn.Close()

		return err
	}

	c.status = StatusEstablishing

	if tunnelMessage.Type != TunnelCreated {
		c.status = StatusError
		c.sdkConfig.OnError(err)
		conn.Close()

		return fmt.Errorf("expected tunnel created message, got %d", tunnelMessage.Type)
	}

	c.localURL = tunnelMessage.Headers[HeaderLocalUrl]
	c.prodURL = tunnelMessage.Headers[HeaderProdUrl]
	c.tunnelID = tunnelMessage.ID

	c.status = StatusConnected
	c.sdkConfig.OnConnected(c.config.LocalPort, c.localURL, c.prodURL, c.tunnelID)

	return nil
}

func (c *TunnelConn) Start() error {
	if err := c.Connect(); err != nil {
		return err
	}

	c.handleTunnelRequests()

	// TODO: handle the local test server later

	return nil
}

func (c *TunnelConn) handleTunnelRequests() {
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
					c.sdkConfig.OnError(err)
					c.errorCh <- err
				} else {
					c.sdkConfig.OnError(errors.New("Error while decoding the message: " + err.Error()))
				}

				c.status = StatusDisconnected
				return
			}

			if msg.Type == TunnelRequest {
				go c.handleLocalRequests(msg)
			} else {
				c.sdkConfig.OnError(fmt.Errorf("Unexpected message type: %d", msg.Type))
			}
		}
	}
}

func (c *TunnelConn) handleLocalRequests(msg TunnelMessage) {
	c.sdkConfig.OnRequest(msg)

	// local target url
	targetURL := fmt.Sprintf("http://localhost:%s%s", c.config.LocalPort, msg.Path)
	req, err := http.NewRequest(msg.Method, targetURL, strings.NewReader(msg.Body))
	if err != nil {
		c.sdkConfig.OnError(errors.New("Error creating request: " + err.Error()))
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
		Timeout: c.config.RequestTimeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			c.sdkConfig.OnError(errors.New("Timeout connecting to the local service: " + err.Error()))
			c.sendErrorResponse(msg.ID, http.StatusGatewayTimeout, "Local service timed out")
		} else {
			c.sdkConfig.OnError(errors.New("Error connecting to the local service: " + err.Error()))
			c.sendErrorResponse(msg.ID, http.StatusBadGateway, "Error connecting to the local service: "+err.Error())
		}

		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.sdkConfig.OnError(errors.New("Error reading the response body: " + err.Error()))
		c.sendErrorResponse(msg.ID, http.StatusInternalServerError, "Failed to read local response body")

		return
	}

	defer resp.Body.Close()

	c.sdkConfig.OnSedingResponse(msg, resp, body)

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
		c.sdkConfig.OnError(errors.New("Error sending response: " + err.Error()))
	}
}

func (c *TunnelConn) sendErrorResponse(requestID string, statusCode int, message string) {
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
		c.sdkConfig.OnError(errors.New("Error sending error oresponse: " + err.Error()))
	}
}

func (c *TunnelConn) Stop() error {
	if c.status == StatusDisconnected {
		return nil
	}

	close(c.errorCh)

	if c.conn != nil {
		c.conn.Close()
	}

	c.status = StatusDisconnected
	c.sdkConfig.OnDisconnected()
	return nil
}
