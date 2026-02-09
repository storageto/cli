package upload

import (
	"context"
	"fmt"
	"hash/crc32"
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
	concurrentFiles  = 6   // Concurrent file uploads (matches web/desktop)
	batchSize        = 250 // Max files per batch API call
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
	var fileCrc uint32
	if initResp.Type == "single" {
		fileCrc, err = u.uploadSingle(ctx, file, initResp.UploadURL, contentType, size)
	} else {
		fileCrc, err = u.uploadMultipart(ctx, file, initResp, size)
	}
	if err != nil {
		return nil, err
	}

	// Confirm upload
	crc32Val := uint64(fileCrc)
	confirmResp, err := u.client.ConfirmUpload(ctx, &api.ConfirmUploadRequest{
		Filename:     filename,
		Size:         size,
		ContentType:  contentType,
		R2Key:        initResp.R2Key,
		CollectionID: collectionID,
		CRC32:        &crc32Val,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to confirm upload: %w", err)
	}

	return confirmResp.File, nil
}

// fileMetadata holds information about a file to upload
type fileMetadata struct {
	path        string
	filename    string
	contentType string
	size        int64
	index       int
	// Set after init
	uploadURL string
	r2Key     string
	uploadErr error
	// Set after upload
	crc32 uint32
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

	// Multiple files - use batch upload with concurrency
	return u.uploadFilesBatch(ctx, paths)
}

// uploadFilesBatch uploads multiple files using batch API endpoints and concurrent R2 uploads
func (u *Uploader) uploadFilesBatch(ctx context.Context, paths []string) (*Result, error) {
	// Step 1: Collect file metadata
	files := make([]*fileMetadata, 0, len(paths))
	for i, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cannot open %s: %w", path, err)
		}

		stat, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("cannot stat %s: %w", path, err)
		}

		contentType := detectContentType(path, file)
		file.Close()

		files = append(files, &fileMetadata{
			path:        path,
			filename:    filepath.Base(path),
			contentType: contentType,
			size:        stat.Size(),
			index:       i,
		})
	}

	// Step 2: Create collection
	collResp, err := u.client.CreateCollection(ctx, &api.CreateCollectionRequest{
		ExpectedFileCount: len(files),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create collection: %w", err)
	}
	collectionID := collResp.Collection.ID
	u.log("Created collection %s for %d files\n", collectionID, len(files))

	// Step 3: Batch init - get presigned URLs for all files
	fmt.Printf("Initializing %d files...\n", len(files))
	for batchStart := 0; batchStart < len(files); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(files) {
			batchEnd = len(files)
		}
		batch := files[batchStart:batchEnd]

		// Build batch request
		batchReq := &api.InitBatchRequest{
			Files: make([]api.BatchFileRequest, len(batch)),
		}
		for i, f := range batch {
			batchReq.Files[i] = api.BatchFileRequest{
				Filename:    f.filename,
				ContentType: f.contentType,
				Size:        f.size,
			}
		}

		// Call init-batch
		initResp, err := u.client.InitUploadBatch(ctx, batchReq)
		if err != nil {
			return nil, fmt.Errorf("failed to init batch: %w", err)
		}

		// Store results
		for i, f := range batch {
			idxStr := strconv.Itoa(i)
			if result, ok := initResp.Results[idxStr]; ok {
				if result.Error != "" {
					f.uploadErr = fmt.Errorf("%s", result.Error)
				} else {
					f.uploadURL = result.UploadURL
					f.r2Key = result.R2Key
				}
			}
		}
	}

	// Step 4: Upload to R2 concurrently (6 at a time)
	fmt.Printf("Uploading %d files (6 concurrent)...\n", len(files))

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrentFiles)
	var uploadedCount int64
	var errorCount int64

	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		if f.uploadErr != nil || f.uploadURL == "" {
			atomic.AddInt64(&errorCount, 1)
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire

		go func(fm *fileMetadata) {
			defer wg.Done()
			defer func() { <-sem }() // Release

			err := u.uploadFileToR2(ctx, fm)
			if err != nil {
				fm.uploadErr = err
				atomic.AddInt64(&errorCount, 1)
			} else {
				n := atomic.AddInt64(&uploadedCount, 1)
				fmt.Printf("\r  Uploaded %d/%d files", n, len(files))
			}
		}(f)
	}
	wg.Wait()
	fmt.Println() // newline after progress

	// Step 5: Batch confirm - create File records
	fmt.Printf("Confirming %d files...\n", uploadedCount)

	// Collect successfully uploaded files
	var toConfirm []*fileMetadata
	for _, f := range files {
		if f.uploadErr == nil && f.r2Key != "" {
			toConfirm = append(toConfirm, f)
		}
	}

	for batchStart := 0; batchStart < len(toConfirm); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(toConfirm) {
			batchEnd = len(toConfirm)
		}
		batch := toConfirm[batchStart:batchEnd]

		// Build confirm request
		confirmReq := &api.ConfirmBatchRequest{
			CollectionID: collectionID,
			Files:        make([]api.BatchConfirmFile, len(batch)),
		}
		for i, f := range batch {
			crc := uint64(f.crc32)
			confirmReq.Files[i] = api.BatchConfirmFile{
				Filename:    f.filename,
				Size:        f.size,
				ContentType: f.contentType,
				R2Key:       f.r2Key,
				CRC32:       &crc,
			}
		}

		// Call confirm-batch
		_, err := u.client.ConfirmUploadBatch(ctx, confirmReq)
		if err != nil {
			return nil, fmt.Errorf("failed to confirm batch: %w", err)
		}
	}

	// Step 6: Mark collection ready
	readyResp, err := u.client.MarkCollectionReady(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("failed to finalize collection: %w", err)
	}

	if errorCount > 0 {
		fmt.Printf("Warning: %d files failed to upload\n", errorCount)
	}

	return &Result{
		Collection:   readyResp.Collection,
		IsCollection: true,
	}, nil
}

// uploadFileToR2 uploads a single file to R2 using a presigned URL
func (u *Uploader) uploadFileToR2(ctx context.Context, fm *fileMetadata) error {
	file, err := os.Open(fm.path)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	defer file.Close()

	fileCrc, err := u.uploadSingle(ctx, file, fm.uploadURL, fm.contentType, fm.size)
	if err != nil {
		return err
	}
	fm.crc32 = fileCrc
	return nil
}

// uploadSingle uploads a file in a single PUT request and returns the CRC-32
func (u *Uploader) uploadSingle(ctx context.Context, file *os.File, uploadURL string, contentType string, size int64) (uint32, error) {
	var fileCrc uint32
	err := u.uploadWithRetry(ctx, func() error {
		file.Seek(0, 0)

		// Create context with timeout for the upload
		uploadCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
		defer cancel()

		pr := &progressReader{
			reader: file,
			total:  size,
			hasher: crc32.IEEETable,
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

		fileCrc = pr.crc
		fmt.Println() // newline after progress
		return nil
	})
	return fileCrc, err
}

// uploadMultipart uploads a file in multiple parts and returns the CRC-32
func (u *Uploader) uploadMultipart(ctx context.Context, file *os.File, initResp *api.InitUploadResponse, size int64) (uint32, error) {
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
				return 0, fmt.Errorf("failed to get upload URLs: %w", err)
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
		return 0, fmt.Errorf("upload cancelled")
	}

	if err := uploadErr.Load(); err != nil {
		return 0, err.(error)
	}

	// Complete multipart upload
	_, err := u.client.CompleteMultipart(ctx, &api.CompleteMultipartRequest{
		UploadID: initResp.UploadID,
		Parts:    parts,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to complete upload: %w", err)
	}

	// Compute CRC-32 by reading the file sequentially from disk
	// This is fast (local disk) compared to the upload itself
	file.Seek(0, 0)
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, file); err != nil {
		return 0, fmt.Errorf("failed to compute CRC-32: %w", err)
	}

	return h.Sum32(), nil
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

// progressReader wraps a reader to track progress and optionally compute CRC-32
type progressReader struct {
	reader     io.Reader
	total      int64
	uploaded   int64
	onProgress func(uploaded, total int64)
	hasher     *crc32.Table
	crc        uint32
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.uploaded += int64(n)
		if pr.hasher != nil {
			pr.crc = crc32.Update(pr.crc, pr.hasher, p[:n])
		}
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
