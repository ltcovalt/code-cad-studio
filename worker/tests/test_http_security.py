from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys

from conftest import WORKER_DIR, http_request, running_worker_server


def test_bearer_auth_enforced_and_loopback_host_checked() -> None:
    with running_worker_server(auth_token="secret-123") as (_state, token, port):
        # 1. Missing Authorization -> 401 + WWW-Authenticate
        status, headers, raw = http_request(port, "GET", "/health")
        assert status == 401
        assert headers.get("www-authenticate") == "Bearer"
        assert json.loads(raw)["ok"] is False

        # 2. Wrong scheme / wrong token -> 401
        status, _, _ = http_request(port, "GET", "/health", headers={"Authorization": "Basic abc"})
        assert status == 401
        status, _, _ = http_request(port, "GET", "/health", headers={"Authorization": "Bearer wrong"})
        assert status == 401

        # 3. Non-loopback / evil Host -> 403 even with valid token
        for bad_host in ("evil.com", "192.168.1.10:8080", "localhost.evil.com", ""):
            status, _, raw = http_request(
                port,
                "GET",
                "/health",
                headers={"Authorization": f"Bearer {token}"},
                host_header=bad_host,
            )
            assert status == 403, f"expected 403 for Host={bad_host!r}, got {status}"
            assert json.loads(raw)["error"] == "forbidden host"

        # 4. Valid loopback Host variants + valid Bearer token -> 200
        for good_host in (f"127.0.0.1:{port}", f"localhost:{port}", f"[::1]:{port}"):
            status, _, raw = http_request(
                port,
                "GET",
                "/health",
                headers={"Authorization": f"Bearer {token}"},
                host_header=good_host,
            )
            assert status == 200, f"expected 200 for Host={good_host!r}, got {status}: {raw!r}"
            payload = json.loads(raw)
            assert payload["ok"] is True
            assert "python_version" in payload
            assert "build123d_version" in payload
            assert "ocp_tessellate_version" in payload


def test_request_body_size_cap_and_json_validation() -> None:
    with running_worker_server(auth_token="tok") as (_state, token, port):
        auth = {"Authorization": f"Bearer {token}"}

        # Oversized Content-Length (> 2 MiB) -> 413 without hanging
        oversize = str(2 * 1024 * 1024 + 1)
        status, _, raw = http_request(
            port,
            "POST",
            "/configure",
            headers={**auth, "Content-Length": oversize},
            body=b"{}",
        )
        assert status == 413
        assert json.loads(raw)["ok"] is False

        # Negative / non-integer Content-Length -> 400
        for bad_len in ("-1", "abc"):
            status, _, _ = http_request(
                port,
                "POST",
                "/configure",
                headers={**auth, "Content-Length": bad_len},
            )
            assert status == 400

        # Chunked transfer encoding -> 411
        status, _, _ = http_request(
            port,
            "POST",
            "/configure",
            headers={**auth, "Transfer-Encoding": "chunked"},
            body=b"0\r\n\r\n",
        )
        assert status == 411

        # Malformed JSON -> 400
        status, _, _ = http_request(port, "POST", "/configure", headers=auth, body=b"{bad")
        assert status == 400

        # Non-object JSON -> 400
        status, _, _ = http_request(port, "POST", "/configure", headers=auth, body=b"[1, 2, 3]")
        assert status == 400


def test_model_id_validation_returns_400_and_unknown_returns_404(tmp_path: Path) -> None:
    with running_worker_server(
        libraries=[{"id": "demo", "name": "Demo", "path": str(tmp_path)}],
        auth_token="tok",
    ) as (_state, token, port):
        auth = {"Authorization": f"Bearer {token}"}

        for bad_id in ("no_slash", "demo%2F..%2Fsecret", "demo%2Fsub%3Aevil", "demo%2F%5Cbad", "demo%2F%2Fpart"):
            status, _, raw = http_request(port, "GET", f"/models/{bad_id}/schema", headers=auth)
            assert status == 400, f"expected 400 for {bad_id!r}, got {status}: {raw!r}"

        status, _, _ = http_request(port, "GET", "/models/demo%2Fmissing_model/schema", headers=auth)
        assert status == 404


def test_worker_subprocess_reads_token_from_env() -> None:
    env = os.environ.copy()
    token = "env-subprocess-token-xyz"
    env["CODE_CAD_WORKER_TOKEN"] = token
    proc = subprocess.Popen(
        [sys.executable, str(WORKER_DIR / "worker.py"), "--port", "0"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    try:
        assert proc.stdout is not None
        ready_line = proc.stdout.readline().strip()
        assert ready_line.startswith("READY ")
        port = int(ready_line.split()[1])

        status, _, _ = http_request(port, "GET", "/health")
        assert status == 401

        status, _, raw = http_request(
            port,
            "GET",
            "/health",
            headers={"Authorization": f"Bearer {token}"},
        )
        assert status == 200
        assert json.loads(raw)["ok"] is True
    finally:
        proc.terminate()
        proc.wait(timeout=5)


def test_duplicate_content_length_and_transfer_encoding_rejected() -> None:
    import http.client

    with running_worker_server(auth_token="tok") as (_state, token, port):
        # 1. Duplicate Content-Length headers -> 400
        conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5.0)
        try:
            conn.putrequest("POST", "/configure")
            conn.putheader("Authorization", f"Bearer {token}")
            conn.putheader("Content-Type", "application/json")
            conn.putheader("Content-Length", "2")
            conn.putheader("Content-Length", "99999999")
            conn.endheaders()
            conn.send(b"{}")
            resp = conn.getresponse()
            body = json.loads(resp.read())
            assert resp.status == 400
            assert "duplicate Content-Length" in body["error"]
        finally:
            conn.close()

        # 2. Any non-empty Transfer-Encoding -> 411
        for te in ("chunked", "gzip", "identity"):
            status, _, body_bytes = http_request(
                port,
                "POST",
                "/configure",
                headers={"Authorization": f"Bearer {token}", "Transfer-Encoding": te},
                body=b"{}",
            )
            assert status == 411, f"expected 411 for Transfer-Encoding={te!r}, got {status}"
            assert json.loads(body_bytes)["ok"] is False

        # 3. Empty POST /reload with omitted Content-Length -> 200 without blocking
        status, _, body_bytes = http_request(
            port,
            "POST",
            "/reload",
            headers={"Authorization": f"Bearer {token}"},
        )
        assert status == 200
        assert json.loads(body_bytes)["ok"] is True
