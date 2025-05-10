package sample

type TunnelMessageType int

const (
	TunnelCreated = iota
	TunnelDestroyed

	TunnelRequest
	TunnelResponse

	TunnelAuthRequest
	TunnelAuthResponse
	TunnelAuthFailure
)

type TunnelMessage struct {
	Type    TunnelMessageType `json:"type"`
	ID      string            `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type TunnelStatus string

const (
	StatusDisconnected   TunnelStatus = "disconnected"
	StatusConnecting     TunnelStatus = "connecting"
	StatusAuthenticating TunnelStatus = "authenticating"
	StatusEstablishing   TunnelStatus = "establishing"
	StatusConnected      TunnelStatus = "connected"
	StatusReconnecting   TunnelStatus = "reconnecting"
	StatusError          TunnelStatus = "error"
)

const (
	HeaderLocalUrl = "Local-URL"
	HeaderProdUrl  = "Prod-URL"
)
