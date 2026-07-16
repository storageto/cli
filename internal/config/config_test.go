package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGetVisitorToken(t *testing.T) {
	// Use temp directory for test
	tmpDir := t.TempDir()

	// Override HOME and XDG_CONFIG_HOME for test isolation. t.Setenv restores
	// the previous values automatically and fails the test if the set errors.
	// (Note: on darwin only HOME is consulted - os.UserConfigDir ignores
	// XDG_CONFIG_HOME outside the Unix branch.)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, ".config"))

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

	// Token file should exist in config dir
	configDir, _ := GetConfigDir()
	tokenPath := filepath.Join(configDir, "token")
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		t.Errorf("token file not created at %s", tokenPath)
	}
}

func TestGetConfigDir(t *testing.T) {
	dir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir() error = %v", err)
	}

	// Config dir should end with "storageto"
	if !strings.HasSuffix(dir, "storageto") {
		t.Errorf("GetConfigDir() = %q, should end with 'storageto'", dir)
	}

	// Platform-specific path checks
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(dir, "Library/Application Support") {
			t.Errorf("GetConfigDir() on macOS = %q, should contain 'Library/Application Support'", dir)
		}
	case "linux":
		if !strings.Contains(dir, ".config") {
			t.Errorf("GetConfigDir() on Linux = %q, should contain '.config'", dir)
		}
	case "windows":
		if !strings.Contains(strings.ToLower(dir), "appdata") {
			t.Errorf("GetConfigDir() on Windows = %q, should contain 'AppData'", dir)
		}
	}
}
