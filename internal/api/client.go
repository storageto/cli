package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/storageto/cli/internal/version"
)

// Client handles API communication with storage.to
type Client struct {
	BaseURL      string
	VisitorToken string
	HTTPClient   *http.Client
}

// NewClient creates a new API client
func NewClient(baseURL string, visitorToken string) *Client {
	return &Client{
		BaseURL:      baseURL,
		VisitorToken: visitorToken,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// InitUploadRequest is sent to /api/upload/init
type InitUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// InitUploadResponse from /api/upload/init
type InitUploadResponse struct {
	Success     bool              `json:"success"`
	Error       string            `json:"error,omitempty"`
	Type        string            `json:"type"` // "single" or "multipart"
	UploadURL   string            `json:"upload_url,omitempty"`
	UploadID    string            `json:"upload_id,omitempty"`
	R2Key       string            `json:"r2_key"`
	PartSize    int64             `json:"part_size,omitempty"`
	TotalParts  int               `json:"total_parts,omitempty"`
	InitialURLs map[string]string `json:"initial_urls,omitempty"`
	Headers     map[string][]string `json:"headers,omitempty"`
}

// GetPartURLsRequest for /api/upload/parts
type GetPartURLsRequest struct {
	UploadID    string `json:"upload_id"`
	PartNumbers []int  `json:"part_numbers"`
}

// GetPartURLsResponse from /api/upload/parts
type GetPartURLsResponse struct {
	Success bool              `json:"success"`
	Error   string            `json:"error,omitempty"`
	URLs    map[string]string `json:"urls"`
}

// CompleteMultipartRequest for /api/upload/complete-multipart
type CompleteMultipartRequest struct {
	UploadID string `json:"upload_id"`
	Parts    []Part `json:"parts"`
}

// Part represents a completed upload part
type Part struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

// CompleteMultipartResponse from /api/upload/complete-multipart
type CompleteMultipartResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ConfirmUploadRequest for /api/upload/confirm
type ConfirmUploadRequest struct {
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	R2Key        string `json:"r2_key"`
	CollectionID string `json:"collection_id,omitempty"`
}

// ConfirmUploadResponse from /api/upload/confirm
type ConfirmUploadResponse struct {
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
	File    *FileInfo `json:"file,omitempty"`
}

// FileInfo contains information about an uploaded file
type FileInfo struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	RawURL    string `json:"raw_url"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	HumanSize string `json:"human_size"`
	ExpiresAt string `json:"expires_at"`
}

// CreateCollectionRequest for /api/collection
type CreateCollectionRequest struct {
	ExpectedFileCount int `json:"expected_file_count,omitempty"`
}

// CreateCollectionResponse from /api/collection
type CreateCollectionResponse struct {
	Success    bool            `json:"success"`
	Error      string          `json:"error,omitempty"`
	Collection *CollectionInfo `json:"collection,omitempty"`
}

// CollectionInfo contains information about a collection
type CollectionInfo struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// MarkCollectionReadyResponse from /api/collection/{id}/ready
type MarkCollectionReadyResponse struct {
	Success    bool            `json:"success"`
	Error      string          `json:"error,omitempty"`
	Collection *CollectionInfo `json:"collection,omitempty"`
}

// Batch upload types

// BatchFileRequest represents a single file in a batch init request
type BatchFileRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// InitBatchRequest for /api/upload/init-batch
type InitBatchRequest struct {
	Files []BatchFileRequest `json:"files"`
}

// InitBatchResult represents the init result for a single file
type InitBatchResult struct {
	Success     bool              `json:"success,omitempty"`
	Error       string            `json:"error,omitempty"`
	Type        string            `json:"type,omitempty"`
	UploadURL   string            `json:"upload_url,omitempty"`
	R2Key       string            `json:"r2_key,omitempty"`
	UploadID    string            `json:"upload_id,omitempty"`
	PartSize    int64             `json:"part_size,omitempty"`
	TotalParts  int               `json:"total_parts,omitempty"`
	InitialURLs map[string]string `json:"initial_urls,omitempty"`
}

// InitBatchResponse from /api/upload/init-batch
type InitBatchResponse struct {
	Success bool                       `json:"success"`
	Error   string                     `json:"error,omitempty"`
	Results map[string]InitBatchResult `json:"results,omitempty"`
}

// BatchConfirmFile represents a single file in a batch confirm request
type BatchConfirmFile struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	R2Key       string `json:"r2_key"`
}

// ConfirmBatchRequest for /api/upload/confirm-batch
type ConfirmBatchRequest struct {
	CollectionID string             `json:"collection_id,omitempty"`
	Files        []BatchConfirmFile `json:"files"`
}

// ConfirmBatchResult represents the confirm result for a single file
type ConfirmBatchResult struct {
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
	File    *FileInfo `json:"file,omitempty"`
}

// ConfirmBatchResponse from /api/upload/confirm-batch
type ConfirmBatchResponse struct {
	Success bool                          `json:"success"`
	Error   string                        `json:"error,omitempty"`
	Results map[string]ConfirmBatchResult `json:"results,omitempty"`
}

// InitUpload initiates an upload
func (c *Client) InitUpload(ctx context.Context, req *InitUploadRequest) (*InitUploadResponse, error) {
	var resp InitUploadResponse
	if err := c.post(ctx, "/api/upload/init", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// GetPartURLs gets presigned URLs for additional parts
func (c *Client) GetPartURLs(ctx context.Context, req *GetPartURLsRequest) (*GetPartURLsResponse, error) {
	var resp GetPartURLsResponse
	if err := c.post(ctx, "/api/upload/parts", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// CompleteMultipart completes a multipart upload
func (c *Client) CompleteMultipart(ctx context.Context, req *CompleteMultipartRequest) (*CompleteMultipartResponse, error) {
	var resp CompleteMultipartResponse
	if err := c.post(ctx, "/api/upload/complete-multipart", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// ConfirmUpload confirms an upload and creates the File record
func (c *Client) ConfirmUpload(ctx context.Context, req *ConfirmUploadRequest) (*ConfirmUploadResponse, error) {
	var resp ConfirmUploadResponse
	if err := c.post(ctx, "/api/upload/confirm", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// CreateCollection creates a new collection for multiple files
func (c *Client) CreateCollection(ctx context.Context, req *CreateCollectionRequest) (*CreateCollectionResponse, error) {
	var resp CreateCollectionResponse
	if err := c.post(ctx, "/api/collection", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// AbortUpload aborts a multipart upload
func (c *Client) AbortUpload(ctx context.Context, uploadID string) error {
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	err := c.post(ctx, "/api/upload/abort", map[string]string{"upload_id": uploadID}, &resp)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// MarkCollectionReady marks a collection as ready
func (c *Client) MarkCollectionReady(ctx context.Context, collectionID string) (*MarkCollectionReadyResponse, error) {
	var resp MarkCollectionReadyResponse
	if err := c.post(ctx, fmt.Sprintf("/api/collection/%s/ready", collectionID), struct{}{}, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// InitUploadBatch initiates uploads for multiple files in a single API call
func (c *Client) InitUploadBatch(ctx context.Context, req *InitBatchRequest) (*InitBatchResponse, error) {
	var resp InitBatchResponse
	if err := c.post(ctx, "/api/upload/init-batch", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// ConfirmUploadBatch confirms multiple uploads in a single API call
func (c *Client) ConfirmUploadBatch(ctx context.Context, req *ConfirmBatchRequest) (*ConfirmBatchResponse, error) {
	var resp ConfirmBatchResponse
	if err := c.post(ctx, "/api/upload/confirm-batch", req, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

func (c *Client) post(ctx context.Context, path string, body interface{}, result interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if c.VisitorToken != "" {
		req.Header.Set("X-Visitor-Token", c.VisitorToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return fmt.Errorf("request cancelled")
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == 429 {
		// Try to parse the rate limit response
		var rateLimitResp struct {
			Error          string `json:"error"`
			Limit          int    `json:"limit"`
			Used           int    `json:"used"`
			ResetsInSeconds int   `json:"resets_in_seconds"`
		}
		if json.Unmarshal(respBody, &rateLimitResp) == nil && rateLimitResp.Error != "" {
			return fmt.Errorf("%s", rateLimitResp.Error)
		}
		return fmt.Errorf("rate limited - please try again later")
	}

	if resp.StatusCode >= 400 {
		// Try to extract error message from JSON response
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &errResp) == nil {
			if errResp.Error != "" {
				return fmt.Errorf("%s", errResp.Error)
			}
			if errResp.Message != "" {
				return fmt.Errorf("%s", errResp.Message)
			}
		}
		return fmt.Errorf("server error (HTTP %d)", resp.StatusCode)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}
