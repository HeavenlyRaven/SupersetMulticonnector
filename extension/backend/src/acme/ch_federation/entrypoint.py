"""Thin backend shim: checks permissions, execs fedctl --json, parses the
response, and audits mutations. No DDL, no validation, no ClickHouse
client — if you find yourself writing logic here, move it to Go.
"""
import json
import logging
import subprocess
from datetime import datetime, timezone

from flask import request
from flask_appbuilder.api import expose, permission_name, protect
from flask_login import current_user
from superset_core.rest_api.api import RestApi
from superset_core.rest_api.decorators import api

FEDCTL_BIN = "/usr/local/bin/fedctl"
FEDCTL_TIMEOUT_SECONDS = 30
# A file write can legitimately take longer than a DDL call, especially for
# a larger sample SQLite file — see cmd/fedctl/sqlitefiles.go's
# maxSQLiteUploadBytes (200 MiB).
FEDCTL_UPLOAD_TIMEOUT_SECONDS = 120

audit_log = logging.getLogger("acme.ch_federation.audit")

# jsonio.Error codes (internal/jsonio/jsonio.go) mapped to HTTP statuses.
ERROR_STATUS = {
    "INVALID_REQUEST": 400,
    "INVALID_NAME": 400,
    "HOST_NOT_ALLOWED": 400,
    "INVALID_PORT": 400,
    "INVALID_PATH": 400,
    "CREDENTIAL_IN_ARGS": 400,
    "UNKNOWN_SOURCE_TYPE": 400,
    "SOURCE_EXISTS": 409,
    "SOURCE_NOT_FOUND": 404,
    "FILE_EXISTS": 409,
    "CONNECTION_FAILED": 502,
    "HUB_UNAVAILABLE": 502,
    "INTERNAL_ERROR": 500,
}


def _exec_fedctl(args, stdin_bytes, timeout):
    """Exec fedctl <args> --json with stdin_bytes on stdin, parse the
    response envelope. subprocess.run with a list, shell=False, an
    explicit timeout, capture_output=True — credentials (and, for an
    upload, file contents) travel only on stdin, never as argv or an
    environment variable."""
    try:
        proc = subprocess.run(
            [FEDCTL_BIN, *args, "--json"],
            input=stdin_bytes,
            capture_output=True,
            shell=False,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        return None, {"code": "INTERNAL_ERROR", "message": "fedctl timed out", "hint": ""}
    try:
        envelope = json.loads(proc.stdout.decode("utf-8") or "{}")
    except ValueError:
        return None, {
            "code": "INTERNAL_ERROR",
            "message": "fedctl returned malformed output",
            "hint": proc.stderr.decode("utf-8", "replace")[:500],
        }
    if envelope.get("ok"):
        return envelope.get("data"), None
    return None, envelope.get("error") or {"code": "INTERNAL_ERROR", "message": "unknown error"}


def _run_fedctl(args, payload=None):
    """JSON-request form of _exec_fedctl — every endpoint except the
    SQLite file upload uses this."""
    return _exec_fedctl(args, json.dumps(payload or {}).encode("utf-8"), FEDCTL_TIMEOUT_SECONDS)


def _run_fedctl_upload(args, raw_bytes):
    """Raw-bytes form of _exec_fedctl, for `source sqlite-files put` —
    stdin is the uploaded file's contents directly, not a JSON envelope."""
    return _exec_fedctl(args, raw_bytes, FEDCTL_UPLOAD_TIMEOUT_SECONDS)


def _error_body(error):
    return {"message": error.get("message", ""), "hint": error.get("hint", "")}, ERROR_STATUS.get(error.get("code"), 500)


def _audit(action, **fields):
    """user, source name, type, host, timestamp — never credentials."""
    audit_log.info(
        "%s user=%s %s",
        action,
        getattr(current_user, "username", "unknown"),
        " ".join(f"{k}={v}" for k, v in fields.items()),
        extra={"timestamp": datetime.now(timezone.utc).isoformat()},
    )


def _can_manage(self):
    return self.appbuilder.sm.has_access("can_manage", "ChFederationAPI")


@api(id="ch_federation_api", name="ClickHouse Federation API",
     description="Manage live federated sources on the ClickHouse hub")
class ChFederationAPI(RestApi):
    # Deliberately no resource_name: live-verified against the installed
    # apache-superset-core package — passing one adds an extra path
    # segment (/extensions/{publisher}/{name}/{resource_name}/{route}),
    # which would make every endpoint below /extensions/acme/ch-federation
    # /ch_federation/... instead of the documented
    # /extensions/acme/ch-federation/... this file's endpoints assume.

    @expose("/source-types", methods=("GET",))
    @protect()
    @permission_name("read")
    def source_types(self):
        data, err = _run_fedctl(["source", "types"])
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        return self.response(200, result=data)

    @expose("/sources", methods=("GET",))
    @protect()
    @permission_name("read")
    def list_sources(self):
        data, err = _run_fedctl(["source", "list"])
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        # canManage lets the UI present as read-only for non-privileged
        # viewers (spec section 11) — the server still enforces the same
        # check independently on every mutating call, this is UX only.
        return self.response(200, result={"sources": data, "canManage": _can_manage(self)})

    @expose("/sources/test", methods=("POST",))
    @protect()
    @permission_name("manage")
    def test_source(self):
        if not _can_manage(self):
            return self.response_403()
        data, err = _run_fedctl(["source", "test"], request.json)
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        return self.response(200, result=data)

    @expose("/sources", methods=("POST",))
    @protect()
    @permission_name("manage")
    def add_source(self):
        if not _can_manage(self):
            return self.response_403()
        body = request.json or {}
        data, err = _run_fedctl(["source", "add"], body)
        if err:
            errbody, status = _error_body(err)
            return self.response(status, **errbody)
        _audit("source.add", name=body.get("name"), type=body.get("type"),
               host=(body.get("params") or {}).get("host", ""))
        return self.response(201, result=data)

    @expose("/sources/<name>", methods=("DELETE",))
    @protect()
    @permission_name("manage")
    def remove_source(self, name):
        if not _can_manage(self):
            return self.response_403()
        data, err = _run_fedctl(["source", "remove"], {"name": name})
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        _audit("source.remove", name=name, type=(data or {}).get("type", ""), host=(data or {}).get("host", ""))
        return self.response(200, result=data)

    @expose("/sources/<name>/tables", methods=("GET",))
    @protect()
    @permission_name("read")
    def source_tables(self, name):
        data, err = _run_fedctl(["source", "tables"], {"name": name})
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        return self.response(200, result=data)

    @expose("/sources/<name>/probe", methods=("POST",))
    @protect()
    @permission_name("read")
    def probe_source(self, name):
        data, err = _run_fedctl(["source", "probe"], {"name": name})
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        return self.response(200, result=data)

    @expose("/sources/sqlite-files", methods=("GET",))
    @protect()
    @permission_name("read")
    def list_sqlite_files(self):
        data, err = _run_fedctl(["source", "sqlite-files", "list"])
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        return self.response(200, result=data)

    @expose("/sources/sqlite-files", methods=("POST",))
    @protect()
    @permission_name("manage")
    def upload_sqlite_file(self):
        # Powers the "Add source" file picker's Upload/Replace buttons: a
        # browser file, no `fedctl seed sqlite` CLI step required.
        # multipart/form-data, not JSON — the file's bytes go straight to
        # fedctl's stdin via _run_fedctl_upload, never buffered as a JSON
        # string. `overwrite=true` is the "Replace" action (SQLite has no
        # live connection to re-read, so a fresh snapshot under the same
        # name is how an operator refreshes a source's data) — without it
        # fedctl refuses to touch an existing file, so a same-named
        # "Upload" can't accidentally clobber one.
        if not _can_manage(self):
            return self.response_403()
        uploaded = request.files.get("file")
        if uploaded is None:
            return self.response(400, message="a \"file\" field is required",
                                  hint="send multipart/form-data with a file part")
        name = (request.form.get("name") or uploaded.filename or "").strip()
        overwrite = request.form.get("overwrite", "").strip().lower() in ("1", "true", "yes")
        args = ["source", "sqlite-files", "put", "--name", name]
        if overwrite:
            args.append("--replace")
        data, err = _run_fedctl_upload(args, uploaded.stream.read())
        if err:
            body, status = _error_body(err)
            return self.response(status, **body)
        _audit("source.sqlite_upload", name=(data or {}).get("name", name), replaced=str(overwrite))
        return self.response(201, result=data)
