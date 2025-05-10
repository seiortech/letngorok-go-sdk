package sdk

import (
	"time"
)

type TunnelConfig struct {
	LocalPort string

	AuthTimeout     time.Duration
	RequestTimeout  time.Duration
	ResponseTimeout time.Duration
}

var DefaultTunnelConfig = TunnelConfig{
	AuthTimeout:     15 * time.Second,
	RequestTimeout:  20 * time.Second,
	ResponseTimeout: 20 * time.Second,
}
