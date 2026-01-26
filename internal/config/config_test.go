package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetVisitorToken(t *testing.T) {
	// Use temp directory for test
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// First call should generate token
	token1, err := GetVisitorToken()
	if err != nil {
		t.Fatalf("GetVisitorToken() error = %v", err)
	}

	// Token should have correct format
	if !strings.HasPrefix(token1, "cli_") {
		t.Errorf("token should start with 'cli_', got %q", token1)
	}

	// Token should be 36 chars (cli_ + 32 hex)
	if len(token1) != 36 {
		t.Errorf("token length = %d, want 36", len(token1))
	}

	// Second call should return same token
	token2, err := GetVisitorToken()
	if err != nil {
		t.Fatalf("GetVisitorToken() second call error = %v", err)
	}

	if token1 != token2 {
		t.Errorf("token changed between calls: %q != %q", token1, token2)
	}

	// Token file should exist
	tokenPath := filepath.Join(tmpDir, ".config", "storageto", "token")
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		t.Errorf("token file not created at %s", tokenPath)
	}
}

func TestGetConfigDir(t *testing.T) {
	dir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir() error = %v", err)
	}

	if !strings.HasSuffix(dir, ".config/storageto") {
		t.Errorf("GetConfigDir() = %q, should end with .config/storageto", dir)
	}
}
