import json
import subprocess
from unittest.mock import MagicMock, patch

import acme.ch_federation.entrypoint as ep


def make_api(can_manage=True):
    instance = ep.ChFederationAPI()
    instance.appbuilder = MagicMock()
    instance.appbuilder.sm.has_access.return_value = can_manage
    return instance


def fake_completed(stdout_obj, returncode=0):
    return subprocess.CompletedProcess(
        args=["fedctl"], returncode=returncode,
        stdout=json.dumps(stdout_obj).encode("utf-8"), stderr=b"",
    )


def test_add_source_denied_without_can_manage_and_never_execs_fedctl():
    api = make_api(can_manage=False)
    with patch("subprocess.run") as run:
        with patch.object(ep, "request", MagicMock(json={"name": "x", "type": "postgres", "params": {}})):
            result = api.add_source()
    assert result["status"] == 403
    run.assert_not_called()


def test_remove_source_denied_without_can_manage():
    api = make_api(can_manage=False)
    with patch("subprocess.run") as run:
        result = api.remove_source("fed_sales")
    assert result["status"] == 403
    run.assert_not_called()


def test_add_source_execs_fedctl_as_argv_list_shell_false():
    api = make_api(can_manage=True)
    body = {"name": "fed_sales", "type": "postgres", "params": {"host": "h", "password": "hunter2"}}
    with patch("subprocess.run", return_value=fake_completed({"ok": True, "data": {"name": "fed_sales"}})) as run:
        with patch.object(ep, "request", MagicMock(json=body)):
            result = api.add_source()
    assert result["status"] == 201
    args, kwargs = run.call_args
    assert isinstance(args[0], list)
    assert args[0][0] == ep.FEDCTL_BIN
    assert kwargs["shell"] is False
    assert kwargs["capture_output"] is True
    assert "timeout" in kwargs
    # Credentials travel on stdin, never as argv.
    assert "hunter2" not in " ".join(args[0])
    sent = json.loads(kwargs["input"].decode("utf-8"))
    assert sent == body


def test_error_envelope_maps_code_to_http_status():
    api = make_api(can_manage=True)
    envelope = {"ok": False, "error": {"code": "HOST_NOT_ALLOWED", "message": "nope", "hint": "add to allowlist"}}
    with patch("subprocess.run", return_value=fake_completed(envelope)):
        with patch.object(ep, "request", MagicMock(json={"name": "x", "type": "postgres", "params": {}})):
            result = api.add_source()
    assert result["status"] == 400
    assert result["message"] == "nope"
    assert result["hint"] == "add to allowlist"


def test_response_never_includes_a_password_field():
    api = make_api(can_manage=True)
    body = {"name": "fed_sales", "type": "postgres", "params": {"host": "h", "password": "hunter2"}}
    envelope = {"ok": True, "data": {"name": "fed_sales", "type": "postgres", "database": "fed_fed_sales"}}
    with patch("subprocess.run", return_value=fake_completed(envelope)):
        with patch.object(ep, "request", MagicMock(json=body)):
            result = api.add_source()
    assert "password" not in json.dumps(result)


def test_list_sources_is_readable_without_can_manage():
    api = make_api(can_manage=False)
    with patch("subprocess.run", return_value=fake_completed({"ok": True, "data": [{"name": "fed_sales"}]})) as run:
        result = api.list_sources()
    assert result["status"] == 200
    run.assert_called_once()
    # canManage lets the frontend present read-only for this user, even
    # though the read itself was allowed.
    assert result["result"]["canManage"] is False
    assert result["result"]["sources"] == [{"name": "fed_sales"}]


def test_list_sources_reports_can_manage_true_for_privileged_user():
    api = make_api(can_manage=True)
    with patch("subprocess.run", return_value=fake_completed({"ok": True, "data": []})):
        result = api.list_sources()
    assert result["result"]["canManage"] is True
