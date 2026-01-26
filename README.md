# storageto

Command-line tool for [storage.to](https://storage.to) - simple, fast file sharing.

## Installation

### Using Go

```bash
go install github.com/ryanbadger/storage.to-cli@latest
```

### From Source

```bash
git clone https://github.com/ryanbadger/storage.to-cli.git
cd storage.to-cli
make build
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

Files larger than 5GB are automatically uploaded in chunks with resumable multipart upload. Progress is shown during upload.

### Options

```
Flags:
  -c, --collection   Force collection even for single file
  -v, --verbose      Show detailed progress
      --api string   API endpoint (default "https://storage.to")
  -h, --help         Show help
```

## Download files

The CLI creates shareable URLs. Anyone can download using:

```bash
# Direct download
curl -LO https://storage.to/r/FQxyz1234

# Or view the download page
open https://storage.to/FQxyz1234
```

## Configuration

The CLI stores a persistent identity token in `~/.config/storageto/token`. This allows:

- Consistent upload tracking across sessions
- Associating uploads with your account if you sign up later

## Limits

Anonymous uploads (no account):
- 20 uploads per day
- 25 GB max file size
- 3-day expiry

Create an account at [storage.to](https://storage.to) for higher limits.

## License

MIT License - see [LICENSE](LICENSE)
