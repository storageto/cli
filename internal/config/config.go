package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

const (
	configDir   = ".config/storageto"
	tokenFile   = "token"
	tokenLength = 16 // 32 hex chars + "cli_" prefix = 36 chars total
)

// GetConfigDir returns the config directory path
func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDir), nil
}

// GetVisitorToken returns the persistent visitor token, creating one if needed
func GetVisitorToken() (string, error) {
	configPath, err := GetConfigDir()
	if err != nil {
		return "", err
	}

	tokenPath := filepath.Join(configPath, tokenFile)

	// Try to read existing token
	data, err := os.ReadFile(tokenPath)
	if err == nil && len(data) > 0 {
		return string(data), nil
	}

	// Generate new token
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	// Ensure config directory exists
	if err := os.MkdirAll(configPath, 0700); err != nil {
		return "", err
	}

	// Save token
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		return "", err
	}

	return token, nil
}

func generateToken() (string, error) {
	bytes := make([]byte, tokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "cli_" + hex.EncodeToString(bytes), nil
}
