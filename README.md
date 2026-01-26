# storageto

**Upload a file, get a link. No buckets.**

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
      --json         Output result as JSON (for scripting)
      --no-token     Run without persistent identity token
      --api string   API endpoint (default "https://storage.to")
  -h, --help         Show help
```

### JSON output

Use `--json` for machine-readable output:

```bash
storageto upload photo.jpg --json
```

```json
{
  "is_collection": false,
  "file_info": {
    "url": "https://storage.to/FQxyz1234",
    "raw_url": "https://storage.to/r/FQxyz1234",
    "size": 2097152,
    "human_size": "2.0 MB",
    "expires_at": "2026-01-29T12:00:00Z"
  }
}
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

The CLI stores a persistent identity token for upload tracking:

- **Location**: `~/.config/storageto/token` (Linux), `~/Library/Application Support/storageto/token` (macOS), `%AppData%\storageto\token` (Windows)
- **What it is**: A random anonymous identifier (not an API key or auth token)
- **What it does**: Links uploads from this machine so you can see "your recent uploads" without signup
- **Privacy**: Delete the token file to reset your identity, or use `--no-token` for fully anonymous uploads

```bash
# Run without any identity tracking
storageto upload photo.jpg --no-token
```

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
