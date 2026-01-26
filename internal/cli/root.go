package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	apiURL  string
	verbose bool
)

var rootCmd = &cobra.Command{
	Use:   "storageto",
	Short: "storage.to CLI - Simple file sharing",
	Long: `Upload and share files via storage.to

Examples:
  storageto upload photo.jpg              Upload a single file
  storageto upload *.log --collection     Upload multiple files as a collection
  storageto upload backup.tar.gz          Large files are automatically chunked`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api", "https://storage.to", "API base URL")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
}
