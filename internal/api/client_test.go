package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// storage.to removed the /r/ hotlink, so raw_url must not appear in the
// marshalled shape that `upload --json` prints.
func TestFileInfoJSONHasNoRawURL(t *testing.T) {
	data, err := json.Marshal(FileInfo{
		ID:        "FQxyz1234",
		URL:       "https://storage.to/FQxyz1234",
		Filename:  "photo.jpg",
		Size:      2097152,
		HumanSize: "2.0 MB",
		ExpiresAt: "2026-01-29T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("json.Marshal(FileInfo) error = %v", err)
	}

	if strings.Contains(string(data), "raw_url") {
		t.Errorf("FileInfo JSON should not contain 'raw_url', got %s", data)
	}

	// The page URL is the only link the CLI surfaces.
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if _, ok := got["raw_url"]; ok {
		t.Error("FileInfo JSON should have no 'raw_url' key")
	}
	if got["url"] != "https://storage.to/FQxyz1234" {
		t.Errorf("url = %v, want the page link", got["url"])
	}
}

// A raw_url in an API response must be ignored, not resurrected onto FileInfo.
func TestFileInfoUnmarshalIgnoresRawURL(t *testing.T) {
	body := `{
		"id": "FQxyz1234",
		"url": "https://storage.to/FQxyz1234",
		"raw_url": "https://storage.to/r/FQxyz1234",
		"filename": "photo.jpg",
		"size": 2097152,
		"human_size": "2.0 MB",
		"expires_at": "2026-01-29T12:00:00Z"
	}`

	var info FileInfo
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if info.URL != "https://storage.to/FQxyz1234" {
		t.Errorf("URL = %q, want the page link", info.URL)
	}

	// Round-tripping must not carry raw_url back out.
	out, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}
	if strings.Contains(string(out), "r/FQxyz1234") {
		t.Errorf("round-trip should drop the raw hotlink, got %s", out)
	}
}
