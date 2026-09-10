# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 AS fedctl-build
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

# Pre-create user_files here (rather than letting the ClickHouse server
# create it on first start, which it does at 0755) so Docker's "seed an
# empty named volume from the image's content at that path" behavior gives
# every fresh deployment a group-writable directory from the start. Group
# 101 (numeric — matches this base image's "clickhouse" group, live-
# verified via `getent group 101`) plus compose.yaml's `group_add: ["101"]`
# on the superset service is what lets the extension's browser-upload
# endpoint (running as the superset image's own UID inside that other
# container, sourceops `sqlite-files put`) write a new SQLite file into
# this same volume without needing to run as the clickhouse UID itself.
RUN mkdir -p /var/lib/clickhouse/user_files \
    && chown 101:101 /var/lib/clickhouse/user_files \
    && chmod 0775 /var/lib/clickhouse/user_files

HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
    CMD ["/usr/local/bin/fedctl", "check", "clickhouse"]
