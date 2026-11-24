from __future__ import annotations

from contextlib import contextmanager
import http.client
import json
from pathlib import Path
import sys
import threading
from http.server import ThreadingHTTPServer
from typing import Any, Iterator

WORKER_DIR = Path(__file__).resolve().parents[1]
REPO_ROOT = WORKER_DIR.parent
if str(WORKER_DIR) not in sys.path:
    sys.path.insert(0, str(WORKER_DIR))

import worker  # noqa: E402


@contextmanager
def running_worker_server(
    libraries: list[dict[str, Any]] | None = None,
    auth_token: str = "test-worker-token",
) -> Iterator[tuple[worker.WorkerState, str, int]]:
    state = worker.WorkerState(libraries)
    server = ThreadingHTTPServer(("127.0.0.1", 0), worker.make_handler(state, auth_token=auth_token))
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield state, auth_token, port
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2.0)


def http_request(
    port: int,
    method: str,
    path: str,
    *,
    headers: dict[str, str] | None = None,
    body: bytes | dict[str, Any] | None = None,
    host_header: str | None = None,
) -> tuple[int, dict[str, str], bytes]:
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5.0)
    try:
        req_headers = dict(headers or {})
        raw_body: bytes | None
        if isinstance(body, dict):
            raw_body = json.dumps(body).encode("utf-8")
            req_headers.setdefault("Content-Type", "application/json")
        else:
            raw_body = body

        conn.putrequest(method, path, skip_host=host_header is not None)
        if host_header is not None:
            if host_header != "":
                conn.putheader("Host", host_header)
        if raw_body is not None and "Content-Length" not in req_headers and "Transfer-Encoding" not in req_headers:
            req_headers["Content-Length"] = str(len(raw_body))
        for k, v in req_headers.items():
            conn.putheader(k, v)
        conn.endheaders()
        if raw_body is not None:
            conn.send(raw_body)

        resp = conn.getresponse()
        resp_body = resp.read()
        resp_headers = {k.lower(): v for k, v in resp.getheaders()}
        return resp.status, resp_headers, resp_body
    finally:
        conn.close()
