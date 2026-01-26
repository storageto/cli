package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestFull(t *testing.T) {
	full := Full()

	// Should contain version
	if !strings.Contains(full, Version) {
		t.Errorf("Full() should contain version %q, got %q", Version, full)
	}

	// Should contain OS/arch
	if !strings.Contains(full, runtime.GOOS) {
		t.Errorf("Full() should contain OS %q, got %q", runtime.GOOS, full)
	}
	if !strings.Contains(full, runtime.GOARCH) {
		t.Errorf("Full() should contain arch %q, got %q", runtime.GOARCH, full)
	}
}

func TestShort(t *testing.T) {
	if Short() != Version {
		t.Errorf("Short() = %q, want %q", Short(), Version)
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent()

	if !strings.HasPrefix(ua, "storageto-cli/") {
		t.Errorf("UserAgent() should start with 'storageto-cli/', got %q", ua)
	}

	if !strings.Contains(ua, runtime.GOOS) {
		t.Errorf("UserAgent() should contain OS, got %q", ua)
	}
}
