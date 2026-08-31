# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.23-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db AS fedctl-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/fedctl ./cmd/fedctl

# Base ClickHouse (pinned tag + digest) plus fedctl — nothing else, per
# spec section 7.
FROM clickhouse/clickhouse-server:26.8.1.2041@sha256:9f8fa0d51f86611fda226c4085cdafdddce35312a60c934de2e4450d61fcb99a

COPY --from=fedctl-build /out/fedctl /usr/local/bin/fedctl
RUN chmod 0755 /usr/local/bin/fedctl

HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
    CMD ["/usr/local/bin/fedctl", "check", "clickhouse"]
