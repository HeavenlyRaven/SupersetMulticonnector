.PHONY: build test fmt vet cross-build clean

MODULE := github.com/acme/superset-federation
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Every recipe below invokes only `go`, `docker`, or `sha256sum` directly —
# no shell scripts, per spec section 3/4.

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o fedctl ./cmd/fedctl

test:
	go test ./...

fmt:
	gofmt -l .

vet:
	go vet ./...

# Cross-compiles all five targets required by spec section 4 and emits a
# checksums.txt (spec section 14: "checksums emitted").
cross-build:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-linux-amd64 ./cmd/fedctl
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-linux-arm64 ./cmd/fedctl
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-darwin-amd64 ./cmd/fedctl
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-darwin-arm64 ./cmd/fedctl
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-windows-amd64.exe ./cmd/fedctl
	cd dist && sha256sum fedctl-* > checksums.txt

clean:
	rm -rf dist fedctl fedctl.exe
