package upload

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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
		_ = file.Close()

		if got != tt.wantMime {
			t.Errorf("detectContentType(%q) = %q, want %q", tt.filename, got, tt.wantMime)
		}
	}
}

// TestUploadPartProgressReportsExactBytes is a regression test for the
// displayed-size bug (issue #1): uploading a .7z showed "121.6 GB / 248.6 MB
// (50083.1%)" for a file far smaller than that. The root cause was that
// progressReader reports the CUMULATIVE bytes read so far on every Read()
// call, but uploadMultipart's shared counter treated each report as an
// INCREMENTAL delta and added it directly - so every chunk re-added the
// whole running total, wildly overcounting. This drives uploadPart against a
// real HTTP server with a body large enough to force multiple Read() calls
// (the net/http client copies request bodies in ~32KB chunks) and asserts
// the sum of everything reported through onProgress equals the part size
// exactly, not some multiple of it.
func TestUploadPartProgressReportsExactBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if n != partSize {
			http.Error(w, "unexpected body size", http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "part.bin")
	data := make([]byte, partSize)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("failed to generate random data: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open test file: %v", err)
	}
	defer func() { _ = file.Close() }()

	u := NewUploader(nil, false)

	var mu sync.Mutex
	var total int64
	var maxSeen int64
	etag, err := u.uploadPart(context.Background(), file, srv.URL, 0, partSize, func(n int64) {
		mu.Lock()
		total += n
		if total > maxSeen {
			maxSeen = total
		}
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("uploadPart failed: %v", err)
	}
	if etag != "etag-1" {
		t.Errorf("etag = %q, want %q", etag, "etag-1")
	}

	if total != partSize {
		t.Errorf("sum of onProgress deltas = %d, want exactly %d (part size) - a mismatch means progress is being over/under-counted", total, partSize)
	}
	if maxSeen != partSize {
		t.Errorf("running total peaked at %d, want it to never exceed %d", maxSeen, partSize)
	}
}

const partSize = 262144 // 256KB - large enough to force several Read() chunks over HTTP
