.PHONY: build clean test install release

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "-s -w \
	-X github.com/ryanbadger/storage.to-cli/internal/version.Version=$(VERSION) \
	-X github.com/ryanbadger/storage.to-cli/internal/version.GitCommit=$(COMMIT) \
	-X github.com/ryanbadger/storage.to-cli/internal/version.BuildDate=$(DATE)"

build:
	go build $(LDFLAGS) -o storageto ./cmd/storageto

install:
	go install $(LDFLAGS) ./cmd/storageto

test:
	go test -v ./...

clean:
	rm -f storageto
	rm -rf dist/

# Cross-compile for releases
release: clean
	mkdir -p dist
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/storageto-darwin-amd64 ./cmd/storageto
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/storageto-darwin-arm64 ./cmd/storageto
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/storageto-linux-amd64 ./cmd/storageto
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/storageto-linux-arm64 ./cmd/storageto
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/storageto-windows-amd64.exe ./cmd/storageto
	cd dist && sha256sum * > checksums.txt
