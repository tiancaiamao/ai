"""conftest.py — pre-install mitmproxy stubs into sys.modules so export_flows can be imported.

Provides real-ish class stubs so that MagicMock(spec=HTTPFlow) works correctly.
"""

import sys
from unittest.mock import MagicMock


class _StubHTTPFlow:
    """Minimal stub for mitmproxy.http.HTTPFlow used in tests."""
    request = None
    response = None

    def __init__(self):
        self.request = MagicMock()
        self.response = MagicMock()


class _StubFlowReader:
    pass


class _StubFlowWriter:
    pass


class _StubFlowReadException(Exception):
    pass


# Build mock module hierarchy with real class stubs for spec support
_http = MagicMock()
_http.HTTPFlow = _StubHTTPFlow

_io = MagicMock()
_io.FlowReader = _StubFlowReader
_io.FlowWriter = _StubFlowWriter

_exceptions = MagicMock()
_exceptions.FlowReadException = _StubFlowReadException

mitmproxy_modules = {
    "mitmproxy": MagicMock(),
    "mitmproxy.io": _io,
    "mitmproxy.exceptions": _exceptions,
    "mitmproxy.http": _http,
}

for mod_name, mod in mitmproxy_modules.items():
    sys.modules.setdefault(mod_name, mod)