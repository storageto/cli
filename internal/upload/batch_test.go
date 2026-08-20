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
}

func newBatchServer() *batchServer {
	return &batchServer{
		putFailure:  map[string]bool{},
		confirmFail: map[string]bool{},
		initErr:     map[int]bool{},
		multipart:   map[int]bool{},
		partsSeen:   map[string]int{},
	}
}

func (b *batchServer) handler(t *testing.T, base *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/collection":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":    true,
				"collection": map[string]any{"id": "col1"},
			})
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
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
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
