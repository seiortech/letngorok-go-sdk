package sdk

import (
	"log"
	"log/slog"
	"net/http"
	"os"
)

type SDKConfig struct {
	TunnelServer string
	AuthToken    string

	OnAuth           func(token string)
	OnConnected      func(localPort, localUrl, prodUrl, tunnelId string)
	OnDisconnected   func()
	OnError          func(err error)
	OnRequest        func(msg TunnelMessage)
	OnSedingResponse func(msg TunnelMessage, resp *http.Response, body []byte)
	Logger           *log.Logger
}

type TunnelClient struct {
	conn   []*TunnelConn
	config *SDKConfig
}

var DefaultSDKConfig = SDKConfig{
	TunnelServer: "tunnel.ngorok.site:9000",
	Logger:       slog.NewLogLogger(slog.NewTextHandler(os.Stdout, nil), slog.LevelInfo),
}

func NewTunnelClient(config *SDKConfig, token string) (TunnelClient, error) {
	if config == nil {
		config = &DefaultSDKConfig
	}

	if config.OnConnected == nil {
		config.OnConnected = func(localPort, localUrl, prodUrl, tunnelId string) {
			config.Logger.Printf("Tunnel established! ID: %s", tunnelId)
			config.Logger.Printf("Local URL: %s, Production URL: %s", localUrl, prodUrl)
			config.Logger.Printf("Forwarding traffic from http://localhost:%s", localPort)
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

	config.AuthToken = token

	return TunnelClient{
		conn:   make([]*TunnelConn, 0),
		config: config,
	}, nil
}

func (c *TunnelClient) Start(port string, config *TunnelConfig) error {
	// for _, conn := range c.conn {
	// 	if conn.LocalPort == port {
	// 		return ErrDuplicatePort
	// 	}
	// }

	if config == nil {
		config = &DefaultTunnelConfig
	}

	// run a new tunnel connection
	conn, err := NewTunnelConn(config, c.config, port)
	if err != nil {
		return err
	}

	conn.Start()

	defer conn.Stop()

	return nil

}
