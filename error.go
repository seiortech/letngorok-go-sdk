package sdk

import "errors"

var (
	ErrNoTokenProvided  = errors.New("no auth token provided and couldn't load from file")
	ErrEmptyToken       = errors.New("stored token is empty")
	ErrNoTokenFilePath  = errors.New("token file path is not set")
	ErrInvalidLocalPort = errors.New("invalid local port")
	ErrAuthFailure      = errors.New("authentication failed")
	ErrConnectionClosed = errors.New("tunnel connection closed")
	ErrTunnelTimeout    = errors.New("tunnel connection timed out")

	ErrDuplicatePort = errors.New("duplicate port")
)
