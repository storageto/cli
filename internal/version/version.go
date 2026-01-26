package version

import (
	"fmt"
	"runtime"
)

// These variables are set at build time via -ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Full returns the full version string
func Full() string {
	return fmt.Sprintf("%s (commit: %s, built: %s, %s/%s)",
		Version, GitCommit, BuildDate, runtime.GOOS, runtime.GOARCH)
}

// Short returns just the version number
func Short() string {
	return Version
}

// UserAgent returns the User-Agent string for HTTP requests
func UserAgent() string {
	return fmt.Sprintf("storageto-cli/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
