package upload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/storageto/cli/internal/api"
)

// singleServer fakes the single-file surface: init, PUT, confirm, then the
// management calls the options add.
type singleServer struct {
	mu          sync.Mutex
	calls       []string // "METHOD path"
	confirmBody map[string]any
	ownerSeen   map[string]string // path -> X-Owner-Token
	refuseMax   bool
	refuseDel   bool
}

func (s *singleServer) handler(base *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		s.ownerSeen[r.URL.Path] = r.Header.Get("X-Owner-Token")
		s.mu.Unlock()
		switch {
		case r.URL.Path == "/api/upload/init":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "type": "single", "upload_url": *base + "/put", "r2_key": "k1",
			})
		case r.URL.Path == "/put":
			w.WriteHeader(200)
		case r.URL.Path == "/api/upload/confirm":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.confirmBody = body
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":     true,
				"file":        map[string]any{"id": "F1", "url": "https://storage.to/F1", "expires_at": "2026-09-01T00:00:00+00:00"},
				"owner_token": "owner-F1",
			})
		case r.URL.Path == "/api/file/F1/max-downloads":
			if s.refuseMax {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "nope"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "max_downloads": 1})
		case r.URL.Path == "/api/file/F1" && r.Method == "DELETE":
			if s.refuseDel {
				w.WriteHeader(403)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "Unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(404)
		}
	}
}

func newSingle(t *testing.T) (*singleServer, *Uploader, string) {
	t.Helper()
	s := &singleServer{ownerSeen: map[string]string{}}
	var base string
	srv := httptest.NewServer(s.handler(&base))
	t.Cleanup(srv.Close)
	base = srv.URL
	path := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No visitor token on purpose: --no-token must still be able to set the
	// cap, via the owner token confirm returned.
	return s, NewUploader(api.NewClient(srv.URL, ""), false), path
}

func TestSingleDefaultsSendNothingExtra(t *testing.T) {
	s, u, path := newSingle(t)
	info, err := u.UploadFile(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.confirmBody["expiry_days"]; ok {
		t.Errorf("confirm sent expiry_days without --expire: %v", s.confirmBody)
	}
	if info.MaxDownloads != 0 {
		t.Errorf("MaxDownloads = %d, want 0", info.MaxDownloads)
	}
	for _, c := range s.calls {
		if strings.Contains(c, "max-downloads") || strings.HasPrefix(c, "DELETE") {
			t.Errorf("unexpected management call %q", c)
		}
	}
}

func TestSingleExpiryAndBurnAfter(t *testing.T) {
	s, u, path := newSingle(t)
	u.Options = Options{ExpiryDays: 1, MaxDownloads: 1}
	info, err := u.UploadFile(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.confirmBody["expiry_days"] != float64(1) {
		t.Errorf("confirm expiry_days = %v, want 1", s.confirmBody["expiry_days"])
	}
	if s.ownerSeen["/api/file/F1/max-downloads"] != "owner-F1" {
		t.Errorf("max-downloads owner token = %q, want owner-F1", s.ownerSeen["/api/file/F1/max-downloads"])
	}
	if info.MaxDownloads != 1 {
		t.Errorf("MaxDownloads = %d, want 1", info.MaxDownloads)
	}
	if info.ExpiresAt != "2026-09-01T00:00:00+00:00" {
		t.Errorf("ExpiresAt = %q", info.ExpiresAt)
	}
}

// A cap that could not be applied must not leave the file up.
func TestSingleBurnAfterFailureRollsBack(t *testing.T) {
	s, u, path := newSingle(t)
	s.refuseMax = true
	u.Options = Options{MaxDownloads: 1}
	_, err := u.UploadFile(context.Background(), path, "")
	if err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "upload removed") {
		t.Fatalf("error = %v, want the server reason and 'upload removed'", err)
	}
	if s.ownerSeen["/api/file/F1"] != "owner-F1" {
		t.Errorf("delete was not sent with the owner token: %v", s.calls)
	}
}

// If even the rollback fails, the error names the live URL so nothing is lost.
func TestSingleRollbackFailureNamesURL(t *testing.T) {
	s, u, path := newSingle(t)
	s.refuseMax, s.refuseDel = true, true
	u.Options = Options{MaxDownloads: 1}
	_, err := u.UploadFile(context.Background(), path, "")
	if err == nil || !strings.Contains(err.Error(), "https://storage.to/F1") || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("error = %v, want the live URL", err)
	}
}

// The rollback failure is a typed error carrying the live URL, so the CLI can
// refuse to turn it into a bare "upload cancelled".
func TestSingleRollbackFailureIsTyped(t *testing.T) {
	s, u, path := newSingle(t)
	s.refuseMax, s.refuseDel = true, true
	u.Options = Options{MaxDownloads: 1}
	_, err := u.UploadFile(context.Background(), path, "")
	var live *LiveUploadError
	if !errors.As(err, &live) || live.URL != "https://storage.to/F1" {
		t.Fatalf("error = %v (%T), want *LiveUploadError with the URL", err, err)
	}
}
