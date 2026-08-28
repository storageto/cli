package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/storageto/cli/internal/api"
	"sync"
	"testing"
)

// batchServer fakes the whole /api/* batch surface.
type batchServer struct {
	mu          sync.Mutex
	putFailure  map[string]bool // r2key -> respond 500 to PUT
	confirmFail map[string]bool // r2key -> confirm rejects
	initErr     map[int]bool    // index -> init error
	multipart   map[int]bool    // index -> multipart response
	completed   []string
	aborted     []string
	partsSeen   map[string]int
	// Options plumbing: every request path in order, the owner token each
	// management call carried, and what confirm-batch was asked for.
	paths         []string
	ownerSeen     map[string]string
	confirmExpiry any
	refuseExpiry  bool
	deleted       []string
}

func newBatchServer() *batchServer {
	return &batchServer{
		putFailure:  map[string]bool{},
		confirmFail: map[string]bool{},
		initErr:     map[int]bool{},
		multipart:   map[int]bool{},
		partsSeen:   map[string]int{},
		ownerSeen:   map[string]string{},
	}
}

func (b *batchServer) handler(t *testing.T, base *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.paths = append(b.paths, r.Method+" "+r.URL.Path)
		if tok := r.Header.Get("X-Owner-Token"); tok != "" {
			b.ownerSeen[r.URL.Path] = tok
		}
		b.mu.Unlock()
		switch {
		case r.URL.Path == "/api/collection":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":     true,
				"collection":  map[string]any{"id": "col1", "url": "https://storage.to/c/col1"},
				"owner_token": "owner-col1",
			})
		case r.URL.Path == "/api/collection/col1/expiry":
			if b.refuseExpiry {
				w.WriteHeader(403)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "Unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "expires_at": "2026-09-01T00:00:00+00:00"})
		case r.URL.Path == "/api/collection/col1/max-downloads":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "max_downloads": 1})
		case r.URL.Path == "/api/collection/col1" && r.Method == "DELETE":
			b.mu.Lock()
			b.deleted = append(b.deleted, "col1")
			b.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case strings.HasSuffix(r.URL.Path, "/ready"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":    true,
				"collection": map[string]any{"id": "col1"},
			})
		case r.URL.Path == "/api/upload/init-batch":
			var req struct {
				Files []map[string]any `json:"files"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			results := map[string]any{}
			for i := range req.Files {
				key := fmt.Sprintf("k%d", i)
				b.mu.Lock()
				isErr, isMp := b.initErr[i], b.multipart[i]
				b.mu.Unlock()
				switch {
				case isErr:
					results[strconv.Itoa(i)] = map[string]any{"success": false, "error": "nope"}
				case isMp:
					results[strconv.Itoa(i)] = map[string]any{
						"success": true, "type": "multipart",
						"upload_id": "u" + key, "r2_key": key,
						"part_size": 5, "total_parts": 3,
						"initial_urls": map[string]string{
							"1": *base + "/put/" + key + "?p=1",
							"2": *base + "/put/" + key + "?p=2",
							"3": *base + "/put/" + key + "?p=3",
						},
					}
				default:
					results[strconv.Itoa(i)] = map[string]any{
						"success": true, "type": "single",
						"upload_url": *base + "/put/" + key, "r2_key": key,
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "results": results})
		case strings.HasPrefix(r.URL.Path, "/put/"):
			key := strings.TrimPrefix(r.URL.Path, "/put/")
			b.mu.Lock()
			fail := b.putFailure[key]
			b.partsSeen[key]++
			b.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				return
			}
			w.Header().Set("ETag", "\"etag-"+r.URL.RawQuery+"\"")
			w.WriteHeader(200)
		case r.URL.Path == "/api/upload/complete-multipart":
			var req struct {
				UploadID string `json:"upload_id"`
				Parts    []struct {
					PartNumber int    `json:"partNumber"`
					ETag       string `json:"etag"`
				} `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			b.mu.Lock()
			b.completed = append(b.completed, req.UploadID)
			b.mu.Unlock()
			if len(req.Parts) != 3 {
				t.Errorf("complete-multipart for %s got %d parts, want 3", req.UploadID, len(req.Parts))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.URL.Path == "/api/upload/abort":
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			b.mu.Lock()
			b.aborted = append(b.aborted, req["upload_id"])
			b.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.URL.Path == "/api/upload/confirm-batch":
			var req struct {
				Files []struct {
					R2Key string `json:"r2_key"`
				} `json:"files"`
				ExpiryDays any `json:"expiry_days"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			b.mu.Lock()
			b.confirmExpiry = req.ExpiryDays
			b.mu.Unlock()
			results := map[string]any{}
			for i, f := range req.Files {
				b.mu.Lock()
				bad := b.confirmFail[f.R2Key]
				b.mu.Unlock()
				if bad {
					results[strconv.Itoa(i)] = map[string]any{"success": false, "error": "blocked"}
				} else {
					results[strconv.Itoa(i)] = map[string]any{"success": true,
						"file": map[string]any{"id": f.R2Key, "url": "https://x/" + f.R2Key}}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "results": results})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}
}

func makeFiles(t *testing.T, n int, size int) []string {
	dir := t.TempDir()
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.bin", i))
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}
	return paths
}

// Half the files fail at init (appended to `failed` from the main loop),
// half fail their PUT (appended from a goroutine under the mutex).
func TestBatchFailedSliceRace(t *testing.T) {
	b := newBatchServer()
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	const n = 60
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			b.putFailure[fmt.Sprintf("k%d", i)] = true
		} else {
			b.initErr[i] = true
		}
	}
	u := NewUploader(api.NewClient(srv.URL, ""), false)
	_, err := u.uploadFilesBatch(context.Background(), makeFiles(t, n, 4))
	if err != nil {
		t.Fatal(err)
	}
}

// A >=part-size file must actually go up in parts and be completed.
func TestBatchMultipartDispatch(t *testing.T) {
	b := newBatchServer()
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	b.multipart[0] = true
	b.multipart[2] = true
	u := NewUploader(api.NewClient(srv.URL, ""), false)
	if _, err := u.uploadFilesBatch(context.Background(), makeFiles(t, 3, 15)); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.completed) != 2 {
		t.Fatalf("completed = %v, want 2 multipart completions", b.completed)
	}
	if b.partsSeen["k0"] == 0 {
		t.Fatalf("no parts uploaded: %v", b.partsSeen)
	}
}

// A multipart file whose parts fail should not leave an orphan session.
func TestBatchMultipartFailureAborts(t *testing.T) {
	b := newBatchServer()
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	b.multipart[0] = true
	b.putFailure["k0"] = true
	u := NewUploader(api.NewClient(srv.URL, ""), false)
	if _, err := u.uploadFilesBatch(context.Background(), makeFiles(t, 1, 15)); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.aborted) == 0 {
		t.Fatalf("failed multipart left an orphan R2 session: aborted=%v", b.aborted)
	}
}

// The options reach the collection: expiry_days rides confirm-batch for the
// member files, the collection row gets its own expiry and cap through the
// management endpoints with the owner token, and all of it lands BEFORE
// ready so the ready response carries the final expiry.
func TestBatchOptionsAppliedBeforeReady(t *testing.T) {
	b := newBatchServer()
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	u := NewUploader(api.NewClient(srv.URL, ""), false)
	u.Options = Options{ExpiryDays: 7, MaxDownloads: 1}
	res, err := u.uploadFilesBatch(context.Background(), makeFiles(t, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	if b.confirmExpiry != float64(7) {
		t.Errorf("confirm-batch expiry_days = %v, want 7", b.confirmExpiry)
	}
	for _, p := range []string{"/api/collection/col1/expiry", "/api/collection/col1/max-downloads"} {
		if b.ownerSeen[p] != "owner-col1" {
			t.Errorf("%s owner token = %q, want owner-col1", p, b.ownerSeen[p])
		}
	}
	idx := func(want string) int {
		for i, p := range b.paths {
			if p == want {
				return i
			}
		}
		t.Fatalf("%s never called: %v", want, b.paths)
		return -1
	}
	ready := idx("POST /api/collection/col1/ready")
	if idx("POST /api/collection/col1/expiry") > ready || idx("POST /api/collection/col1/max-downloads") > ready {
		t.Errorf("management calls after ready: %v", b.paths)
	}
	if res.Collection.MaxDownloads != 1 {
		t.Errorf("Collection.MaxDownloads = %d, want 1", res.Collection.MaxDownloads)
	}
	if len(b.deleted) != 0 {
		t.Errorf("collection deleted on the happy path: %v", b.paths)
	}
}

// Defaults change nothing on the wire.
func TestBatchDefaultsSendNothingExtra(t *testing.T) {
	b := newBatchServer()
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	u := NewUploader(api.NewClient(srv.URL, ""), false)
	if _, err := u.uploadFilesBatch(context.Background(), makeFiles(t, 2, 4)); err != nil {
		t.Fatal(err)
	}
	if b.confirmExpiry != nil {
		t.Errorf("confirm-batch sent expiry_days by default: %v", b.confirmExpiry)
	}
	for _, p := range b.paths {
		if strings.Contains(p, "/expiry") || strings.Contains(p, "max-downloads") || strings.HasPrefix(p, "DELETE") {
			t.Errorf("unexpected management call %q", p)
		}
	}
}

// A collection that did not get its settings is removed, never handed out.
func TestBatchOptionFailureRollsBack(t *testing.T) {
	b := newBatchServer()
	b.refuseExpiry = true
	var base string
	srv := httptest.NewServer(b.handler(t, &base))
	defer srv.Close()
	base = srv.URL

	u := NewUploader(api.NewClient(srv.URL, ""), false)
	u.Options = Options{ExpiryDays: 1}
	_, err := u.uploadFilesBatch(context.Background(), makeFiles(t, 2, 4))
	if err == nil || !strings.Contains(err.Error(), "upload removed") {
		t.Fatalf("error = %v, want 'upload removed'", err)
	}
	if len(b.deleted) != 1 || b.ownerSeen["/api/collection/col1"] != "owner-col1" {
		t.Errorf("collection not deleted with the owner token: %v", b.paths)
	}
	for _, p := range b.paths {
		if strings.HasSuffix(p, "/ready") {
			t.Errorf("ready was called after the settings failed: %v", b.paths)
		}
	}
}
