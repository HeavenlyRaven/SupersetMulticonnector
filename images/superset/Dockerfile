# syntax=docker/dockerfile:1

# Stage 1: build fedctl for the target platform. --platform=$BUILDPLATFORM
# pins this stage to the host's native platform so the Go compiler itself
# never runs under emulation; TARGETOS/TARGETARCH drive cross-compilation
# of the *output* binary instead (spec section 7).
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

# Stage 2: derive from Superset's official release image. Superset ships
# almost no database drivers by design — deriving and adding
# clickhouse-connect is the documented production path (spec section 7).
FROM apache/superset:6.1.0@sha256:59cd4af66006fe4cc98906eda42a771dbefdacb432f9ab083e02cdc6ff01f29d

USER root

# clickhouse-connect: the SQLAlchemy dialect Superset uses to query the
# ClickHouse hub as a data source (section 12).
# psycopg2-binary: live-verified this is also required — the base image
# ships no PostgreSQL driver at all, so without this Superset can't even
# connect to its own external metadata database (SQLALCHEMY_DATABASE_URI)
# and fails at startup with "ModuleNotFoundError: No module named
# 'psycopg2'", before ever getting to ClickHouse.
#
# Installed with `uv` (the tool this base image itself is built with),
# not plain `pip` — also live-verified: Superset runs from a dedicated
# venv at /app/.venv whose bin/python is a symlink back to the system
# interpreter, so plain `pip --python /app/.venv/bin/python` resolves
# that symlink and silently installs into system site-packages instead —
# same failure as installing nothing. `uv pip install --python
# /app/.venv/bin/python` correctly recognizes and targets the venv's own
# site-packages regardless.
RUN uv pip install --python /app/.venv/bin/python \
    "clickhouse-connect[sqlalchemy]==1.7.2" \
    "psycopg2-binary==2.9.13"

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
