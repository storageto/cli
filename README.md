# storageto

Command-line tool for [storage.to](https://storage.to) - simple, fast file sharing.

## Installation

### Using Go

```bash
go install github.com/ryanbadger/storage.to-cli/cmd/storageto@latest
```

Make sure `~/go/bin` is in your PATH:
```bash
export PATH="$PATH:$HOME/go/bin"
```

### From Source

```bash
git clone https://github.com/ryanbadger/storage.to-cli.git
cd storage.to-cli
make install
```

### Pre-built Binaries

Download from [Releases](https://github.com/ryanbadger/storage.to-cli/releases).

## Usage

### Upload a single file

```bash
storageto upload photo.jpg
```

Output:
```
URL:     https://storage.to/FQxyz1234
Raw:     https://storage.to/r/FQxyz1234
Size:    2.1 MB
Expires: 2026-01-29T12:00:00Z
```

- **URL** - Human-friendly download page
- **Raw** - Direct download link (for `curl`, `wget`, scripts)

### Upload multiple files

Multiple files are automatically grouped into a collection:

```bash
storageto upload file1.txt file2.txt file3.txt
```

Or use glob patterns:

```bash
storageto upload *.log
storageto upload src/**/*.go
```

### Large files

Files larger than 5GB are automatically uploaded in chunks with resumable multipart upload. Progress is shown during upload:

```
  1.2 GB / 10.0 GB (12.0%)
```

Press Ctrl+C to cancel - partial uploads are cleaned up automatically.

### Options

```
Flags:
  -c, --collection   Force collection even for single file
  -v, --verbose      Show detailed progress
      --api string   API endpoint (default "https://storage.to")
  -h, --help         Show help
```

## Downloading Files

The CLI creates shareable URLs. Anyone can download:

```bash
# Direct download (follows redirect to file)
curl -LO https://storage.to/r/FQxyz1234

# Check file info first
curl -I https://storage.to/r/FQxyz1234

# Download collection as JSON manifest
curl https://storage.to/c/FQabc5678.json
```

## Configuration

The CLI stores a persistent identity token in `~/.config/storageto/token`. This:

- Tracks your uploads consistently across sessions
- Allows associating uploads with your account if you sign up later
- Is automatically generated on first use

## Limits

**Anonymous CLI uploads** (no account):

| Limit | Value |
|-------|-------|
| Uploads per day | 20 |
| Max file size | 25 GB |
| File expiry | 3 days |

**With account**: Higher limits based on your plan. See [storage.to/pricing](https://storage.to/pricing).

## Development

### Building from source

```bash
git clone https://github.com/ryanbadger/storage.to-cli.git
cd storage.to-cli
make build      # Build binary
make test       # Run tests
make install    # Install to ~/go/bin
```

### Project structure

```
├── cmd/
│   └── storageto/
│       └── main.go         # Entry point
├── internal/
│   ├── api/                # API client
│   ├── cli/                # CLI commands (cobra)
│   ├── config/             # Config and token management
│   ├── upload/             # Upload logic (single + multipart)
│   └── version/            # Version info (set at build time)
├── Makefile                # Build with version injection
└── README.md
```

### Building releases

```bash
make release
```

Creates binaries for:
- macOS (Intel + Apple Silicon)
- Linux (amd64 + arm64)
- Windows (amd64)

## License

MIT License - see [LICENSE](LICENSE)
