.PHONY: build test fmt vet cross-build installer-windows packages-linux version-check version-set clean

MODULE := github.com/acme/superset-federation

# The VERSION file is the single source of truth; `make version-set
# VERSION=x.y.z` propagates it everywhere and `make version-check`
# verifies nothing has drifted. See tools/version and RELEASING.md.
VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Inno Setup's compiler, for `installer-windows`. Override if it isn't on
# your PATH:
#   make installer-windows ISCC="C:/Program Files (x86)/Inno Setup 6/ISCC.exe"
ISCC ?= ISCC.exe

# Linux packages must carry a real semver, so they read the VERSION file
# directly rather than inheriting VERSION's "dev" fallback.
PKG_VERSION ?= $(shell cat VERSION 2>/dev/null || echo 0.0.0)

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

# Builds the per-user Windows installer. Requires Inno Setup 6.3+ and so
# only runs on Windows; see installer/windows/README.md. The binary is
# rebuilt first so the installer can never ship a stale dist/ artifact.
installer-windows:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fedctl-windows-amd64.exe ./cmd/fedctl
	"$(ISCC)" installer/windows/superset-federation.iss

# Builds .deb, .rpm and .apk for both Linux architectures from
# packaging/nfpm.yaml. Requires nfpm and the linux binaries from
# cross-build:
#   go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
packages-linux: cross-build
	ARCH=amd64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager deb --target dist/
	ARCH=amd64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager rpm --target dist/
	ARCH=amd64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager apk --target dist/
	ARCH=arm64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager deb --target dist/
	ARCH=arm64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager rpm --target dist/
	ARCH=arm64 VERSION=$(PKG_VERSION) nfpm package --config packaging/nfpm.yaml --packager apk --target dist/

# Verifies every file carrying a copy of the version agrees with VERSION.
# Run before tagging; the release workflow runs it too and refuses to
# publish a tag that disagrees.
version-check:
	go run ./tools/version check

# Bumps the version everywhere at once:
#   make version-set VERSION=0.1.2
version-set:
	go run ./tools/version set $(VERSION)

clean:
	rm -rf dist fedctl fedctl.exe
