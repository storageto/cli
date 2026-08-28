package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/storageto/cli/internal/api"
	"github.com/storageto/cli/internal/config"
	"github.com/storageto/cli/internal/upload"
	"github.com/spf13/cobra"
)

var (
	collection   bool
	jsonOutput   bool
	expire       string
	burnAfter    bool
	maxDownloads int
)

var uploadCmd = &cobra.Command{
	Use:   "upload <file> [files...]",
	Short: "Upload files to storage.to",
	Long: `Upload one or more files to storage.to and get shareable links.

Examples:
  storageto upload photo.jpg                    # Single file
  storageto upload doc.pdf image.png            # Multiple files (auto-collection)
  storageto upload *.log --collection           # Explicit collection
  storageto upload backup.tar.gz                # Large files auto-chunk
  storageto upload secret.pdf --expire 1d       # Gone after one day (1d-7d)
  storageto upload secret.pdf --burn-after      # Gone after the first download
  storageto upload build.zip --max-downloads 5  # Gone after five downloads`,
	Args: cobra.MinimumNArgs(1),
	RunE: runUpload,
}

func init() {
	rootCmd.AddCommand(uploadCmd)
	uploadCmd.Flags().BoolVarP(&collection, "collection", "c", false, "Create a collection for multiple files")
	uploadCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	uploadCmd.Flags().StringVar(&expire, "expire", "", fmt.Sprintf("Lifetime in days, 1d to %dd (default 3d)", upload.MaxExpiryDays))
	uploadCmd.Flags().BoolVar(&burnAfter, "burn-after", false, "Delete after the first download (same as --max-downloads 1)")
	uploadCmd.Flags().IntVar(&maxDownloads, "max-downloads", 0, "Delete after this many downloads (1-1000)")
}

// parseExpire turns the --expire value into whole days. Accepts "3", "3d" or
// "3D"; refuses hours explicitly because the API only has day granularity,
// and refuses anything over MaxExpiryDays because the server would cap it
// silently and a script would never know.
func parseExpire(value string) (int, error) {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" {
		return 0, nil
	}
	if strings.HasSuffix(v, "h") {
		return 0, fmt.Errorf("--expire is in whole days (1d to %dd), hours are not supported", upload.MaxExpiryDays)
	}
	v = strings.TrimSuffix(v, "d")
	days, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--expire must be a number of days like 1d or 7d, got %q", value)
	}
	if days < 1 || days > upload.MaxExpiryDays {
		return 0, fmt.Errorf("--expire must be between 1d and %dd, got %s", upload.MaxExpiryDays, value)
	}
	return days, nil
}

// uploadOptions validates the lifetime/download flags together. maxSet says
// whether --max-downloads was given at all: an explicit 0 is a mistake to
// refuse, not "unlimited".
func uploadOptions(expire string, burnAfter bool, maxDownloads int, maxSet bool) (upload.Options, error) {
	days, err := parseExpire(expire)
	if err != nil {
		return upload.Options{}, err
	}
	if maxSet && (maxDownloads < 1 || maxDownloads > 1000) {
		return upload.Options{}, fmt.Errorf("--max-downloads must be between 1 and 1000, got %d", maxDownloads)
	}
	if burnAfter {
		if maxSet && maxDownloads != 1 {
			return upload.Options{}, fmt.Errorf("--burn-after means --max-downloads 1; drop one of them")
		}
		maxDownloads = 1
	}
	return upload.Options{ExpiryDays: days, MaxDownloads: maxDownloads}, nil
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

	opts, err := uploadOptions(expire, burnAfter, maxDownloads, cmd.Flags().Changed("max-downloads"))
	if err != nil {
		return err
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
	uploader.Options = opts

	// Do the upload
	result, err := uploader.UploadFiles(ctx, files, asCollection)
	if err != nil {
		// A live-but-uncapped upload outranks the cancel message: Ctrl+C
		// between confirm and the settings call is exactly how it happens.
		var live *upload.LiveUploadError
		if errors.As(err, &live) {
			return err
		}
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
			if result.Collection.MaxDownloads > 0 {
				fmt.Printf("Downloads:  %s\n", describeMaxDownloads(result.Collection.MaxDownloads))
			}
		} else {
			fmt.Printf("URL:     %s\n", result.FileInfo.URL)
			fmt.Printf("Size:    %s\n", result.FileInfo.HumanSize)
			fmt.Printf("Expires: %s\n", result.FileInfo.ExpiresAt)
			if result.FileInfo.MaxDownloads > 0 {
				fmt.Printf("Downloads: %s\n", describeMaxDownloads(result.FileInfo.MaxDownloads))
			}
		}
	}

	// A partial upload is not a success. Exiting 0 here is what let
	// `storageto upload * -c && rm *` delete the originals after files had
	// silently failed - the collection URL printed above is real, it is just
	// short. The failures are on stderr (and in `failed` for --json); this is
	// the status a script actually branches on.
	if len(result.Failed) > 0 {
		return fmt.Errorf("%d file(s) did not upload", len(result.Failed))
	}

	return nil
}

func describeMaxDownloads(max int) string {
	if max == 1 {
		return "1 (burn after reading)"
	}
	return fmt.Sprintf("max %d", max)
}
