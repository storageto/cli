package upload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	}

	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestGeneratePartNumbers(t *testing.T) {
	tests := []struct {
		start, end int
		wantLen    int
	}{
		{1, 1, 1},
		{1, 5, 5},
		{10, 15, 6},
	}

	for _, tt := range tests {
		got := generatePartNumbers(tt.start, tt.end)
		if len(got) != tt.wantLen {
			t.Errorf("generatePartNumbers(%d, %d) len = %d, want %d", tt.start, tt.end, len(got), tt.wantLen)
		}
		if got[0] != tt.start {
			t.Errorf("generatePartNumbers(%d, %d)[0] = %d, want %d", tt.start, tt.end, got[0], tt.start)
		}
		if got[len(got)-1] != tt.end {
			t.Errorf("generatePartNumbers(%d, %d)[last] = %d, want %d", tt.start, tt.end, got[len(got)-1], tt.end)
		}
	}
}

func TestMin(t *testing.T) {
	if min(1, 2) != 1 {
		t.Error("min(1, 2) should be 1")
	}
	if min(5, 3) != 3 {
		t.Error("min(5, 3) should be 3")
	}
	if min(4, 4) != 4 {
		t.Error("min(4, 4) should be 4")
	}
}

func TestDetectContentType(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()

	tests := []struct {
		filename string
		content  string
		wantMime string
	}{
		{"test.jpg", "fake jpeg", "image/jpeg"},
		{"test.png", "fake png", "image/png"},
		{"test.pdf", "fake pdf", "application/pdf"},
		{"test.json", `{"key": "value"}`, "application/json"},
		{"test.txt", "plain text", "text/plain"},
		{"test.go", "package main", "text/x-go"},
	}

	for _, tt := range tests {
		path := filepath.Join(tmpDir, tt.filename)
		if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("failed to open test file: %v", err)
		}

		got := detectContentType(path, file)
		file.Close()

		if got != tt.wantMime {
			t.Errorf("detectContentType(%q) = %q, want %q", tt.filename, got, tt.wantMime)
		}
	}
}
