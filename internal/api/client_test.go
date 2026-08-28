package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// The management calls run after the upload is live and must prove ownership
// with the owner token confirm handed back, because --no-token leaves the
// visitor token empty and the server's ownership ladder has nothing else to
// go on.
func TestManagementCallsSendOwnerTokenAndBody(t *testing.T) {
	type seen struct {
		method, path, owner, visitor string
		body                         map[string]any
	}
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, seen{r.Method, r.URL.Path, r.Header.Get("X-Owner-Token"), r.Header.Get("X-Visitor-Token"), body})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ctx := context.Background()
	if err := c.SetFileMaxDownloads(ctx, "F1", "tok-f", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.SetFileExpiry(ctx, "F1", "tok-f", 2); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteFile(ctx, "F1", "tok-f"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCollectionExpiry(ctx, "C1", "tok-c", 7); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCollectionMaxDownloads(ctx, "C1", "tok-c", 5); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCollection(ctx, "C1", "tok-c"); err != nil {
		t.Fatal(err)
	}

	want := []seen{
		{"POST", "/api/file/F1/max-downloads", "tok-f", "", map[string]any{"max_downloads": float64(1)}},
		{"POST", "/api/file/F1/expiry", "tok-f", "", map[string]any{"days": float64(2)}},
		{"DELETE", "/api/file/F1", "tok-f", "", map[string]any{}},
		{"POST", "/api/collection/C1/expiry", "tok-c", "", map[string]any{"days": float64(7)}},
		{"POST", "/api/collection/C1/max-downloads", "tok-c", "", map[string]any{"max_downloads": float64(5)}},
		{"DELETE", "/api/collection/C1", "tok-c", "", map[string]any{}},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(want), calls)
	}
	for i := range want {
		if calls[i].method != want[i].method || calls[i].path != want[i].path || calls[i].owner != want[i].owner || calls[i].visitor != want[i].visitor {
			t.Errorf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
		if fmt.Sprint(calls[i].body) != fmt.Sprint(want[i].body) {
			t.Errorf("call %d body = %v, want %v", i, calls[i].body, want[i].body)
		}
	}
}

// A management call that the server refuses surfaces the server's message,
// and a 403 (not the owner) is not mistaken for success.
func TestManagementCallSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "Unauthorized"})
	}))
	defer srv.Close()
	err := NewClient(srv.URL, "").SetFileMaxDownloads(context.Background(), "F1", "", 1)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("error = %v, want the server's Unauthorized", err)
	}
}

// expiry_days and max_downloads are omitted from the wire and from --json
// output when unset, so a default upload's requests and output are unchanged.
func TestOptionalFieldsOmittedWhenZero(t *testing.T) {
	for name, v := range map[string]any{
		"ConfirmUploadRequest": ConfirmUploadRequest{Filename: "a", R2Key: "k"},
		"ConfirmBatchRequest":  ConfirmBatchRequest{Files: []BatchConfirmFile{}},
		"FileInfo":             FileInfo{ID: "F1"},
		"CollectionInfo":       CollectionInfo{ID: "C1"},
	} {
		data, _ := json.Marshal(v)
		for _, key := range []string{"expiry_days", "max_downloads"} {
			if strings.Contains(string(data), key) {
				t.Errorf("%s zero value marshals %q: %s", name, key, data)
			}
		}
	}
	data, _ := json.Marshal(ConfirmUploadRequest{Filename: "a", R2Key: "k", ExpiryDays: 3})
	if !strings.Contains(string(data), `"expiry_days":3`) {
		t.Errorf("ConfirmUploadRequest with ExpiryDays lacks the field: %s", data)
	}
	data, _ = json.Marshal(FileInfo{ID: "F1", MaxDownloads: 1})
	if !strings.Contains(string(data), `"max_downloads":1`) {
		t.Errorf("FileInfo with MaxDownloads lacks the field: %s", data)
	}
}
