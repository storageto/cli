package upload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/storageto/cli/internal/api"
	"github.com/storageto/cli/internal/version"
)

const (
	maxRetries       = 3
	retryDelay       = 2 * time.Second
	concurrentParts  = 4
	partURLBatchSize = 50
	uploadTimeout    = 30 * time.Minute
)

// Uploader handles file uploads to storage.to
type Uploader struct {
	client  *api.Client
	verbose bool
}

// NewUploader creates a new uploader
func NewUploader(client *api.Client, verbose bool) *Uploader {
	return &Uploader{
		client:  client,
		verbose: verbose,
	}
}

// Result contains the upload result
type Result struct {
	FileInfo     *api.FileInfo
	Collection   *api.CollectionInfo
	IsCollection bool
}

// UploadFile uploads a single file
func (u *Uploader) UploadFile(ctx context.Context, path string, collectionID string) (*api.FileInfo, error) {
	// Check for cancellation
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Open and stat file
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot read file info: %w", err)
	}

	filename := filepath.Base(path)
	contentType := detectContentType(path, file)
	size := stat.Size()

	// Reset file position after content type detection
	file.Seek(0, 0)

	u.log("Uploading %s (%s)\n", filename, humanSize(size))

	// Initialize upload
	initResp, err := u.client.InitUpload(ctx, &api.InitUploadRequest{
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize upload: %w", err)
	}

	// Upload based on type
	if initResp.Type == "single" {
		err = u.uploadSingle(ctx, file, initResp.UploadURL, contentType, size)
	} else {
		err = u.uploadMultipart(ctx, file, initResp, size)
	}
	if err != nil {
		return nil, err
	}

	// Confirm upload
	confirmResp, err := u.client.ConfirmUpload(ctx, &api.ConfirmUploadRequest{
		Filename:     filename,
		Size:         size,
		ContentType:  contentType,
		R2Key:        initResp.R2Key,
		CollectionID: collectionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to confirm upload: %w", err)
	}

	return confirmResp.File, nil
}

// UploadFiles uploads multiple files, optionally as a collection
func (u *Uploader) UploadFiles(ctx context.Context, paths []string, asCollection bool) (*Result, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no files specified")
	}

	// Single file, no collection
	if len(paths) == 1 && !asCollection {
		fileInfo, err := u.UploadFile(ctx, paths[0], "")
		if err != nil {
			return nil, err
		}
		return &Result{FileInfo: fileInfo}, nil
	}

	// Multiple files or explicit collection
	collResp, err := u.client.CreateCollection(ctx, &api.CreateCollectionRequest{
		ExpectedFileCount: len(paths),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create collection: %w", err)
	}

	u.log("Created collection %s\n", collResp.Collection.ID)

	// Upload each file
	for i, path := range paths {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("upload cancelled")
		}

		fmt.Printf("[%d/%d] ", i+1, len(paths))
		_, err := u.UploadFile(ctx, path, collResp.Collection.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to upload %s: %w", filepath.Base(path), err)
		}
	}

	// Mark collection ready
	readyResp, err := u.client.MarkCollectionReady(ctx, collResp.Collection.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to finalize collection: %w", err)
	}

	return &Result{
		Collection:   readyResp.Collection,
		IsCollection: true,
	}, nil
}

// uploadSingle uploads a file in a single PUT request
func (u *Uploader) uploadSingle(ctx context.Context, file *os.File, uploadURL string, contentType string, size int64) error {
	return u.uploadWithRetry(ctx, func() error {
		file.Seek(0, 0)

		// Create context with timeout for the upload
		uploadCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
		defer cancel()

		pr := &progressReader{
			reader: file,
			total:  size,
			onProgress: func(uploaded, total int64) {
				u.printProgress(uploaded, total)
			},
		}

		req, err := http.NewRequestWithContext(uploadCtx, "PUT", uploadURL, pr)
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", version.UserAgent())
		req.ContentLength = size

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			if uploadCtx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("upload timed out")
			}
			if ctx.Err() == context.Canceled {
				return fmt.Errorf("upload cancelled")
			}
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		fmt.Println() // newline after progress
		return nil
	})
}

// uploadMultipart uploads a file in multiple parts
func (u *Uploader) uploadMultipart(ctx context.Context, file *os.File, initResp *api.InitUploadResponse, size int64) error {
	u.log("Multipart upload: %d parts, %s each\n", initResp.TotalParts, humanSize(initResp.PartSize))

	// Abort cleanup on cancellation
	defer func() {
		if ctx.Err() != nil && initResp.UploadID != "" {
			// Best effort abort - use background context since main ctx is cancelled
			abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			u.client.AbortUpload(abortCtx, initResp.UploadID)
			u.log("Cleaned up partial upload\n")
		}
	}()

	// Track completed parts
	var parts []api.Part
	var partsMu sync.Mutex
	var uploadedBytes int64
	var uploadedMu sync.Mutex

	// Semaphore for concurrent uploads
	sem := make(chan struct{}, concurrentParts)
	var wg sync.WaitGroup
	var uploadErr atomic.Value

	// Upload all parts
	for partNum := 1; partNum <= initResp.TotalParts; partNum++ {
		// Check for cancellation or previous error
		if ctx.Err() != nil {
			break
		}
		if uploadErr.Load() != nil {
			break
		}

		// Get URL for this part
		partNumStr := strconv.Itoa(partNum)
		url, ok := initResp.InitialURLs[partNumStr]
		if !ok {
			// Fetch more URLs
			moreURLs, err := u.client.GetPartURLs(ctx, &api.GetPartURLsRequest{
				UploadID:    initResp.UploadID,
				PartNumbers: generatePartNumbers(partNum, min(partNum+partURLBatchSize-1, initResp.TotalParts)),
			})
			if err != nil {
				return fmt.Errorf("failed to get upload URLs: %w", err)
			}
			// Merge into initResp for future use
			for k, v := range moreURLs.URLs {
				initResp.InitialURLs[k] = v
			}
			url = moreURLs.URLs[partNumStr]
		}

		// Calculate part boundaries
		offset := int64(partNum-1) * initResp.PartSize
		partSize := initResp.PartSize
		if partNum == initResp.TotalParts {
			partSize = size - offset // Last part may be smaller
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(pNum int, pURL string, pOffset, pSize int64) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			etag, err := u.uploadPart(ctx, file, pURL, pOffset, pSize, func(n int64) {
				uploadedMu.Lock()
				uploadedBytes += n
				u.printProgress(uploadedBytes, size)
				uploadedMu.Unlock()
			})

			if err != nil {
				uploadErr.CompareAndSwap(nil, fmt.Errorf("part %d failed: %w", pNum, err))
				return
			}

			partsMu.Lock()
			parts = append(parts, api.Part{
				PartNumber: pNum,
				ETag:       etag,
			})
			partsMu.Unlock()
		}(partNum, url, offset, partSize)
	}

	wg.Wait()
	fmt.Println() // newline after progress

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("upload cancelled")
	}

	if err := uploadErr.Load(); err != nil {
		return err.(error)
	}

	// Complete multipart upload
	_, err := u.client.CompleteMultipart(ctx, &api.CompleteMultipartRequest{
		UploadID: initResp.UploadID,
		Parts:    parts,
	})
	if err != nil {
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	return nil
}

// uploadPart uploads a single part and returns its ETag
func (u *Uploader) uploadPart(ctx context.Context, file *os.File, url string, offset, size int64, onProgress func(int64)) (string, error) {
	var etag string

	err := u.uploadWithRetry(ctx, func() error {
		// Create context with timeout
		uploadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		// Create section reader for this part
		section := io.NewSectionReader(file, offset, size)

		req, err := http.NewRequestWithContext(uploadCtx, "PUT", url, &progressReader{
			reader:     section,
			total:      size,
			onProgress: func(uploaded, _ int64) { onProgress(uploaded) },
		})
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", version.UserAgent())
		req.ContentLength = size

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			if uploadCtx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("part upload timed out")
			}
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("part upload failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		// Extract ETag from response
		etag = strings.Trim(resp.Header.Get("ETag"), "\"")
		if etag == "" {
			return fmt.Errorf("server did not return ETag")
		}

		return nil
	})

	return etag, err
}

// uploadWithRetry retries an upload function
func (u *Uploader) uploadWithRetry(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = fn()
		if err == nil {
			return nil
		}

		if i < maxRetries-1 {
			u.log("Retry %d/%d: %v\n", i+1, maxRetries-1, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
			}
		}
	}
	return err
}

func (u *Uploader) log(format string, args ...interface{}) {
	if u.verbose {
		fmt.Printf(format, args...)
	}
}

func (u *Uploader) printProgress(uploaded, total int64) {
	pct := float64(uploaded) / float64(total) * 100
	fmt.Printf("\r  %s / %s (%.1f%%)  ", humanSize(uploaded), humanSize(total), pct)
}

// progressReader wraps a reader to track progress
type progressReader struct {
	reader     io.Reader
	total      int64
	uploaded   int64
	onProgress func(uploaded, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.uploaded += int64(n)
		if pr.onProgress != nil {
			pr.onProgress(pr.uploaded, pr.total)
		}
	}
	return n, err
}

func detectContentType(path string, file *os.File) string {
	// Try by extension first
	ext := strings.ToLower(filepath.Ext(path))
	mimeTypes := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".pdf":  "application/pdf",
		".zip":  "application/zip",
		".tar":  "application/x-tar",
		".gz":   "application/gzip",
		".tgz":  "application/gzip",
		".bz2":  "application/x-bzip2",
		".xz":   "application/x-xz",
		".7z":   "application/x-7z-compressed",
		".rar":  "application/vnd.rar",
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".ogg":  "audio/ogg",
		".flac": "audio/flac",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".json": "application/json",
		".xml":  "application/xml",
		".html": "text/html",
		".css":  "text/css",
		".js":   "application/javascript",
		".ts":   "application/typescript",
		".go":   "text/x-go",
		".py":   "text/x-python",
		".rb":   "text/x-ruby",
		".rs":   "text/x-rust",
		".c":    "text/x-c",
		".cpp":  "text/x-c++",
		".h":    "text/x-c",
		".hpp":  "text/x-c++",
		".java": "text/x-java",
		".php":  "text/x-php",
		".sh":   "application/x-sh",
		".sql":  "application/sql",
		".yml":  "application/x-yaml",
		".yaml": "application/x-yaml",
		".toml": "application/toml",
	}

	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}

	// Detect from content
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	file.Seek(0, 0)

	return http.DetectContentType(buf[:n])
}

func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func generatePartNumbers(start, end int) []int {
	nums := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		nums = append(nums, i)
	}
	return nums
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
