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
    "CONNECTION_FAILED": 502,
    "HUB_UNAVAILABLE": 502,
    "INTERNAL_ERROR": 500,
}


def _run_fedctl(args, payload=None):
    """Exec fedctl <args> --json, request body on stdin, parse the
    response envelope. subprocess.run with a list, shell=False, an
    explicit timeout, capture_output=True — credentials travel only on
    stdin, never as argv or an environment variable."""
    try:
        proc = subprocess.run(
            [FEDCTL_BIN, *args, "--json"],
            input=json.dumps(payload or {}).encode("utf-8"),
            capture_output=True,
            shell=False,
            timeout=FEDCTL_TIMEOUT_SECONDS,
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
    resource_name = "ch_federation"

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
