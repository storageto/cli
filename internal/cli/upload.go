package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ryanbadger/storage.to-cli/internal/api"
	"github.com/ryanbadger/storage.to-cli/internal/config"
	"github.com/ryanbadger/storage.to-cli/internal/upload"
	"github.com/spf13/cobra"
)

var (
	collection bool
	jsonOutput bool
)

var uploadCmd = &cobra.Command{
	Use:   "upload <file> [files...]",
	Short: "Upload files to storage.to",
	Long: `Upload one or more files to storage.to and get shareable links.

Examples:
  storageto upload photo.jpg                    # Single file
  storageto upload doc.pdf image.png            # Multiple files (auto-collection)
  storageto upload *.log --collection           # Explicit collection
  storageto upload backup.tar.gz                # Large files auto-chunk`,
	Args: cobra.MinimumNArgs(1),
	RunE: runUpload,
}

func init() {
	rootCmd.AddCommand(uploadCmd)
	uploadCmd.Flags().BoolVarP(&collection, "collection", "c", false, "Create a collection for multiple files")
	uploadCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
}

func runUpload(cmd *cobra.Command, args []string) error {
	// Set up context with cancellation for Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nCancelling upload...")
		cancel()
	}()

	// Expand globs and validate files
	var files []string
	for _, pattern := range args {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			// Try as literal path
			if _, err := os.Stat(pattern); err != nil {
				return fmt.Errorf("file not found: %s", pattern)
			}
			matches = []string{pattern}
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				return fmt.Errorf("cannot access %s: %w", match, err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory (use storageto upload %s/* for contents)", match, match)
			}
			files = append(files, match)
		}
	}

	if len(files) == 0 {
		return fmt.Errorf("no files to upload")
	}

	// Auto-collection for multiple files
	asCollection := collection || len(files) > 1

	// Get visitor token (unless --no-token is set)
	var visitorToken string
	if !noToken {
		var err error
		visitorToken, err = config.GetVisitorToken()
		if err != nil {
			return fmt.Errorf("failed to initialize: %w", err)
		}
	}

	// Create client and uploader
	client := api.NewClient(apiURL, visitorToken)
	uploader := upload.NewUploader(client, verbose)

	// Do the upload
	result, err := uploader.UploadFiles(ctx, files, asCollection)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("upload cancelled")
		}
		return err
	}

	// Print result
	if jsonOutput {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else {
		fmt.Println()
		if result.IsCollection {
			fmt.Printf("Collection: %s\n", result.Collection.URL)
			fmt.Printf("Expires:    %s\n", result.Collection.ExpiresAt)
		} else {
			fmt.Printf("URL:     %s\n", result.FileInfo.URL)
			fmt.Printf("Raw:     %s\n", result.FileInfo.RawURL)
			fmt.Printf("Size:    %s\n", result.FileInfo.HumanSize)
			fmt.Printf("Expires: %s\n", result.FileInfo.ExpiresAt)
		}
	}

	return nil
}
