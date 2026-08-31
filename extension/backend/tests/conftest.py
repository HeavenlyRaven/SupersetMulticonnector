"""Stubs for the Superset/Flask-AppBuilder packages entrypoint.py imports.

The real apache-superset-core / flask-appbuilder stack is heavy and not
installed in this test environment; these stubs give entrypoint.py just
enough surface (decorators that no-op, a RestApi base with response()/
response_403()) to unit test its own logic — permission gating, the
fedctl subprocess contract, and error-code mapping — independent of the
real framework.
"""
import sys
import types
from unittest.mock import MagicMock


def _noop_decorator_factory(*_args, **_kwargs):
    def deco(fn):
        return fn
    return deco


def install_stub_modules():
    flask_mod = types.ModuleType("flask")
    flask_mod.request = MagicMock()
    sys.modules["flask"] = flask_mod

    fab_api_mod = types.ModuleType("flask_appbuilder.api")
    fab_api_mod.expose = _noop_decorator_factory
    fab_api_mod.protect = _noop_decorator_factory
    fab_api_mod.permission_name = _noop_decorator_factory
    fab_pkg = types.ModuleType("flask_appbuilder")
    fab_pkg.api = fab_api_mod
    sys.modules["flask_appbuilder"] = fab_pkg
    sys.modules["flask_appbuilder.api"] = fab_api_mod

    flask_login_mod = types.ModuleType("flask_login")
    flask_login_mod.current_user = MagicMock(username="tester")
    sys.modules["flask_login"] = flask_login_mod

    class StubRestApi:
        def response(self, code, **kwargs):
            return {"status": code, **kwargs}

        def response_403(self):
            return {"status": 403}

    rest_api_api_mod = types.ModuleType("superset_core.rest_api.api")
    rest_api_api_mod.RestApi = StubRestApi
    rest_api_decorators_mod = types.ModuleType("superset_core.rest_api.decorators")
    rest_api_decorators_mod.api = _noop_decorator_factory

    sys.modules["superset_core"] = types.ModuleType("superset_core")
    sys.modules["superset_core.rest_api"] = types.ModuleType("superset_core.rest_api")
    sys.modules["superset_core.rest_api.api"] = rest_api_api_mod
    sys.modules["superset_core.rest_api.decorators"] = rest_api_decorators_mod

    return flask_mod


install_stub_modules()

sys.path.insert(0, str(__import__("pathlib").Path(__file__).resolve().parents[1] / "src"))
