"""Superset configuration for the two-container ClickHouse federation stack.

See the README for the full rationale — this file only implements it.
Every value that could be a secret comes from the environment; nothing
here is hardcoded.
"""

import os
import sys

from flask_caching.backends.filesystemcache import FileSystemCache


def _require_env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        print(
            f"superset_config: {name} is not set. Superset's metadata database "
            "must be the external PostgreSQL instance; refusing to start rather "
            "than silently falling back to SQLite. See .env.example.",
            file=sys.stderr,
        )
        sys.exit(1)
    return value


# --- Metadata database -------------------------------------------------
# Spec section 1: no Postgres container, no SQLite fallback. Fail loudly
# and immediately if this is unset or points at SQLite — a wrong metadata
# store here is a reliability incident, not a style question.
SQLALCHEMY_DATABASE_URI = _require_env("SQLALCHEMY_DATABASE_URI")
if SQLALCHEMY_DATABASE_URI.lower().startswith("sqlite"):
    print(
        "superset_config: SQLALCHEMY_DATABASE_URI points at SQLite. "
        "This stack requires the external PostgreSQL instance for metadata.",
        file=sys.stderr,
    )
    sys.exit(1)

SECRET_KEY = _require_env("SUPERSET_SECRET_KEY")

# --- Cache: Redis is gone, see README section "What was removed" -------
# FileSystemCache on superset_home (the only volume this container owns)
# replaces the Redis-backed cache, plus ClickHouse's own use_query_cache
# with a short TTL (users.d/roles.xml) for repeated dashboard loads.
_CACHE_DIR = "/app/superset_home/cache"
os.makedirs(_CACHE_DIR, exist_ok=True)

CACHE_CONFIG = {
    "CACHE_TYPE": "FileSystemCache",
    "CACHE_DIR": os.path.join(_CACHE_DIR, "superset"),
    "CACHE_DEFAULT_TIMEOUT": 300,
    "CACHE_THRESHOLD": 5000,
}
DATA_CACHE_CONFIG = {
    "CACHE_TYPE": "FileSystemCache",
    "CACHE_DIR": os.path.join(_CACHE_DIR, "data"),
    "CACHE_DEFAULT_TIMEOUT": 300,
    "CACHE_THRESHOLD": 5000,
}

# --- No Celery: async query results backend and broker are both gone ---
# SQL Lab runs synchronously. CELERY_CONFIG and RESULTS_BACKEND are
# intentionally left unset (Superset's own defaults are already "off").
# This is why SQLLAB_TIMEOUT below matters so much: a long query now
# holds a gunicorn worker for its whole duration.
#
# Alerts & Reports and thumbnails both require Celery — see README's
# known-limitations section for the re-expansion path if these become
# requirements.
FEATURE_FLAGS = {
    "ALERT_REPORTS": False,
    "THUMBNAILS": False,
    "ENABLE_EXTENSIONS": True,
}

# --- Timeouts: sized generously because queries now hold workers -------
SQLLAB_TIMEOUT = int(os.environ.get("SQLLAB_TIMEOUT", "300"))
SUPERSET_WEBSERVER_TIMEOUT = int(os.environ.get("SUPERSET_WEBSERVER_TIMEOUT", "300"))
# WEB_CONCURRENCY is read by the base image's gunicorn launcher, not by
# Superset's own Python config — documented here anyway so the worker
# count and the timeout above are read together. See .env.example.
WEB_CONCURRENCY = int(os.environ.get("WEB_CONCURRENCY", "8"))

# --- Extensions (spec sections 10-11) -----------------------------------
# Pre-1.0 framework (developer-docs marks it "Next"/unreleased) — see the
# README's known-limitations section. EXTENSIONS_PATH matches
# images/superset/Dockerfile's `/app/extensions` (built .supx bundles);
# compose.dev.yaml's dev profile instead wires LOCAL_EXTENSIONS at the
# extension's source tree for iteration without a bundle rebuild.
EXTENSIONS_PATH = os.environ.get("EXTENSIONS_PATH", "/app/extensions")
LOCAL_EXTENSIONS = os.environ.get("LOCAL_EXTENSIONS", "")

# --- fedctl / ClickHouse hub coordinates, read by the extension's shim --
CLICKHOUSE_HOST = os.environ.get("CLICKHOUSE_HOST", "clickhouse")
CLICKHOUSE_HTTP_PORT = int(os.environ.get("CLICKHOUSE_HTTP_PORT", "8123"))
