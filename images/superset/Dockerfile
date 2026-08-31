# syntax=docker/dockerfile:1

# Stage 1: build fedctl for the target platform. --platform=$BUILDPLATFORM
# pins this stage to the host's native platform so the Go compiler itself
# never runs under emulation; TARGETOS/TARGETARCH drive cross-compilation
# of the *output* binary instead (spec section 7).
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

# Stage 2: derive from Superset's official release image. Superset ships
# almost no database drivers by design — deriving and adding
# clickhouse-connect is the documented production path (spec section 7).
FROM apache/superset:6.1.0@sha256:59cd4af66006fe4cc98906eda42a771dbefdacb432f9ab083e02cdc6ff01f29d

USER root

RUN pip install --no-cache-dir "clickhouse-connect[sqlalchemy]==1.7.2"

COPY --from=fedctl-build /out/fedctl /usr/local/bin/fedctl
RUN chmod 0755 /usr/local/bin/fedctl

COPY superset/superset_config.py /app/pythonpath/superset_config.py

# Production extensions load from a directory of built .supx bundles
# (spec section 10's "deployment" reference: EXTENSIONS_PATH + a
# `superset-extensions bundle` build product). The dev-time alternative —
# LOCAL_EXTENSIONS pointing straight at extension/ source — is wired up
# in compose.dev.yaml instead, so a production image never depends on
# the extension's source tree being present at build time.
RUN mkdir -p /app/extensions
ENV EXTENSIONS_PATH=/app/extensions

USER superset

HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=5 \
    CMD ["/usr/local/bin/fedctl", "check", "superset"]
