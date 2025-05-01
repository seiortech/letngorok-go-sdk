package sdk

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	LocalPort     string
	TunnelServer  string
	AuthToken     string
	TokenFilePath string
	Debug         bool
}

func DefaultConfig(tunnelServer string) *Config {
	homeDir, _ := os.UserHomeDir()
	return &Config{
		TunnelServer:  tunnelServer,
		TokenFilePath: filepath.Join(homeDir, ".ngorok", "auth.token"),
		Debug:         false,
	}
}

func (c *Config) loadAuthToken() (string, error) {
	if c.AuthToken != "" {
		return c.AuthToken, nil
	}

	if c.TokenFilePath == "" {
		return "", ErrNoTokenProvided
	}

	tokenBytes, err := os.ReadFile(c.TokenFilePath)
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return "", ErrEmptyToken
	}

	return token, nil
}

func (c *Config) saveAuthToken(token string) error {
	if c.TokenFilePath == "" {
		return ErrNoTokenFilePath
	}

	dir := filepath.Dir(c.TokenFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	return os.WriteFile(c.TokenFilePath, []byte(token), 0600)
}
