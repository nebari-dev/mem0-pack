import asyncio
import time

import pytest
from starlette.responses import PlainTextResponse
from starlette.testclient import TestClient

from mem0pack.health import Health


class DummyApp:
    def __init__(self):
        self.calls = []

    async def __call__(self, scope, receive, send):
        self.calls.append((scope["type"], scope.get("method"), scope.get("path")))
        await PlainTextResponse("inner")(scope, receive, send)


def test_health_answers_without_calling_app():
    app = DummyApp()
    client = TestClient(Health(app, ready=lambda: True))
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}
    assert resp.headers["content-type"] == "application/json"
    assert app.calls == []


@pytest.mark.parametrize(
    "ready,status,body",
    [(True, 200, {"status": "ok"}), (False, 503, {"status": "not ready"})],
)
def test_readyz_reflects_ready(ready, status, body):
    app = DummyApp()
    client = TestClient(Health(app, ready=lambda: ready))
    resp = client.get("/readyz")
    assert resp.status_code == status
    assert resp.json() == body
    assert resp.headers["content-type"] == "application/json"
    assert app.calls == []


@pytest.mark.parametrize(
    "method,path",
    [
        ("GET", "/"),
        ("GET", "/healthz/"),
        ("GET", "/healthz/x"),
        ("POST", "/healthz"),
        ("POST", "/readyz"),
    ],
)
def test_health_passes_other_paths_through(method, path):
    app = DummyApp()
    client = TestClient(Health(app, ready=lambda: True))
    resp = client.request(method, path)
    assert resp.text == "inner"
    assert len(app.calls) == 1


@pytest.mark.parametrize("scope_type", ["lifespan", "websocket"])
def test_non_http_scopes_pass_through(scope_type):
    seen = []

    async def app(scope, receive, send):
        seen.append(scope["type"])

    scope = {"type": scope_type, "method": "GET", "path": "/healthz"}
    asyncio.run(Health(app, ready=lambda: True)(scope, None, None))
    assert seen == [scope_type]


def _call(app, path):
    """Drive one ASGI GET and return (status, elapsed seconds)."""
    sent = []

    async def send(msg):
        sent.append(msg)

    async def run():
        start = time.monotonic()
        await app({"type": "http", "method": "GET", "path": path}, None, send)
        return sent[0]["status"], time.monotonic() - start

    return run()


def test_readyz_times_out_without_blocking_healthz():
    app = Health(DummyApp(), ready=lambda: time.sleep(1) or True, timeout=0.2)

    async def both():
        return await asyncio.gather(_call(app, "/readyz"), _call(app, "/healthz"))

    (ready_status, ready_elapsed), (health_status, health_elapsed) = asyncio.run(both())
    assert ready_status == 503
    assert ready_elapsed < 0.8
    assert health_status == 200
    assert health_elapsed < 0.1


def test_readyz_exception_means_not_ready():
    def boom():
        raise RuntimeError("db down")

    status, _ = asyncio.run(_call(Health(DummyApp(), ready=boom), "/readyz"))
    assert status == 503
