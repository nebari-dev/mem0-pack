import json

import pytest
from starlette.testclient import TestClient

from mem0pack.allowlist import RULES, Allowlist, Rule, scope_error_body, scope_error_query

UID = "123e4567-e89b-12d3-a456-426614174000"
MAX_BODY = 1024 * 1024


class Recorder:
    """Downstream ASGI app: records calls and echoes the body bytes it received."""

    def __init__(self):
        self.calls = []

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            self.calls.append((scope["type"], None, None))
            return
        body = b""
        while True:
            msg = await receive()
            body += msg.get("body", b"")
            if not msg.get("more_body"):
                break
        self.calls.append((scope["type"], scope["method"], scope["path"]))
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"application/octet-stream")],
            }
        )
        await send({"type": "http.response.body", "body": body})


@pytest.fixture
def app():
    return Recorder()


@pytest.fixture
def client(app):
    return TestClient(Allowlist(app), raise_server_exceptions=True)


def send(client, method, target, body=None):
    kwargs = {} if body is None else {"content": body}
    return client.request(method, target, **kwargs)


SEARCH_OK = json.dumps({"query": "q", "filters": {"user_id": "alice"}}).encode()


@pytest.mark.parametrize(
    "method,target,body",
    [
        ("POST", "/memories", b'{"messages":[{"role":"user","content":"hi"}],"user_id":"a"}'),
        ("GET", "/memories?user_id=alice", None),
        ("GET", "/memories?agent_id=helper&top_k=5", None),
        ("GET", "/memories?run_id=r1&show_expired=true", None),
        ("GET", f"/memories/{UID}", None),
        ("DELETE", f"/memories/{UID}", None),
        ("POST", "/search", SEARCH_OK),
        ("POST", "/search", b'{"query":"q","user_id":"alice"}'),
        ("POST", "/search", b'{"query":"q","agent_id":"a","filters":{"run_id":"r"}}'),
        ("POST", "/search", b'{"query":"q","filters":{"user_id":"alice"},"top_k":3}'),
    ],
)
def test_allowed_reaches_app_with_original_body(client, app, method, target, body):
    resp = send(client, method, target, body)
    assert resp.status_code == 200
    assert resp.content == (body or b"")
    assert len(app.calls) == 1


NOT_FOUND = [
    ("POST", "/auth/register"),
    ("POST", "/auth/login"),
    ("POST", "/configure"),
    ("POST", "/reset"),
    ("GET", "/api-keys"),
    ("GET", "/entities"),
    ("GET", "/requests"),
    ("GET", "/docs"),
    ("GET", "/openapi.json"),
    ("GET", "/redoc"),
    ("POST", "/generate-instructions"),
    ("GET", f"/memories/{UID}/history"),
    ("PUT", f"/memories/{UID}"),
    ("DELETE", "/memories?user_id=alice"),
    ("DELETE", "/memories"),
    ("GET", "/memories/not-a-uuid"),
    ("GET", "/memories/"),
    ("GET", "/Memories?user_id=a"),
    ("GET", "/memories/%2e%2e/reset"),
    ("GET", "/memories/%2E%2E/reset"),
    ("GET", f"/memories/{UID.upper()}"),
    ("GET", f"/memories/{UID}%0a"),
    ("GET", f"/memories/{UID}/"),
    ("GET", "//memories?user_id=a"),
    ("GET", "/%6demories?user_id=a"),
    ("POST", f"/memories/{UID}"),
    ("PATCH", "/search"),
    ("GET", "/search"),
    ("PUT", "/memories"),
    ("GET", "/healthz"),
    ("GET", "/readyz"),
    ("GET", "/"),
    ("GET", ""),
]


@pytest.mark.parametrize("method,target", NOT_FOUND)
def test_not_allowlisted_is_404_and_app_not_called(client, app, method, target):
    resp = send(client, method, target or "/")
    assert resp.status_code == 404
    assert resp.content == b'{"detail":"Not Found"}'
    assert resp.headers["content-type"] == "application/json"
    assert app.calls == []


def j(obj):
    return json.dumps(obj).encode()


BAD_REQUEST = [
    ("GET", "/memories", None),
    ("GET", "/memories?user_id=", None),
    ("GET", "/memories?user_id=*", None),
    ("GET", "/memories?user_id=a&user_id=b", None),
    ("GET", "/memories?user_id=a&limit=5", None),
    ("GET", "/memories?user_id=a&AND=x", None),
    ("GET", "/memories?user_id=a&agent_id=", None),
    ("GET", "/memories?top_k=5", None),
    ("GET", "/memories?%75ser_id=a&user_id=b", None),
    ("GET", "/memories?user_id=a&top_k=1&top_k=2", None),
    ("POST", "/search", j({"query": "q"})),
    ("POST", "/search", j({"query": "q", "filters": {}})),
    ("POST", "/search", j({"query": "q", "filters": {"user_id": "*"}})),
    ("POST", "/search", j({"query": "q", "filters": {"user_id": ""}})),
    ("POST", "/search", j({"query": "q", "user_id": "*"})),
    ("POST", "/search", j({"query": "q", "user_id": ""})),
    ("POST", "/search", j({"query": "q", "user_id": 5})),
    ("POST", "/search", j({"query": "q", "user_id": "a", "agent_id": "*"})),
    ("POST", "/search", j({"query": "q", "filters": {"OR": [{"user_id": "a"}, {"user_id": "b"}]}})),
    ("POST", "/search", j({"query": "q", "user_id": "a", "filters": {"NOT": {"user_id": "b"}}})),
    ("POST", "/search", j({"query": "q", "filters": {"user_id": {"in": ["a", "b"]}}})),
    ("POST", "/search", j({"query": "q", "filters": {"user_id": "a", "category": "x"}})),
    ("POST", "/search", j({"query": "q", "filters": {"user_id": ["a"]}})),
    ("POST", "/search", j({"query": "q", "filters": ["user_id"]})),
    ("POST", "/search", j({"query": "q", "filters": "user_id"})),
    ("POST", "/search", j(["user_id"])),
    ("POST", "/search", b'{"query":"q","filters":{"user_id":"a","user_id":"*"}}'),
    ("POST", "/search", b"not json"),
    ("POST", "/search", b""),
    ("POST", "/search", b"\xff\xfe"),
    ("POST", "/search", b'{"query":"' + b"a" * MAX_BODY + b'","user_id":"a"}'),
    ("POST", "/memories", b"x" * (MAX_BODY + 1)),
]


@pytest.mark.parametrize("method,target,body", BAD_REQUEST)
def test_unscoped_or_malformed_is_400_and_app_not_called(client, app, method, target, body):
    resp = send(client, method, target, body)
    assert resp.status_code == 400
    assert resp.headers["content-type"] == "application/json"
    assert list(resp.json()) == ["detail"]
    assert isinstance(resp.json()["detail"], str) and resp.json()["detail"]
    assert app.calls == []


def test_body_at_cap_is_accepted(client, app):
    body = b"x" * MAX_BODY
    resp = send(client, "POST", "/memories", body)
    assert resp.status_code == 200
    assert resp.content == body


def test_oversize_body_message_and_app_not_called(client, app):
    resp = client.post("/memories", content=b"x" * (MAX_BODY + 1))
    assert resp.status_code == 400
    assert "too large" in resp.json()["detail"]
    assert app.calls == []


@pytest.mark.parametrize("scope_type", ["lifespan", "websocket"])
def test_non_http_scopes_pass_through(app, scope_type):
    import asyncio

    async def run():
        await Allowlist(app)({"type": scope_type}, None, None)

    asyncio.run(run())
    assert app.calls == [(scope_type, None, None)]


def test_missing_raw_path_falls_back_to_path(app):
    import asyncio

    sent = []

    async def snd(m):
        sent.append(m)

    async def rcv():
        return {"type": "http.request", "body": b"", "more_body": False}

    scope = {
        "type": "http",
        "method": "DELETE",
        "path": f"/memories/{UID}",
        "query_string": b"",
    }
    asyncio.run(Allowlist(app)(scope, rcv, snd))
    assert app.calls == [("http", "DELETE", f"/memories/{UID}")]


def test_custom_rules_are_used(app):
    import re

    rules = (Rule("GET", re.compile(r"/only\Z"), "none"),)
    client = TestClient(Allowlist(app, rules))
    assert client.get("/only").status_code == 200
    assert client.get("/memories?user_id=a").status_code == 404


def test_default_rules_cover_exactly_the_table():
    assert {(r.method, r.scope) for r in RULES} == {
        ("POST", "none"),
        ("GET", "query"),
        ("GET", "none"),
        ("DELETE", "none"),
        ("POST", "body"),
    }
    assert len(RULES) == 5


@pytest.mark.parametrize(
    "params,ok",
    [
        ([("user_id", "a")], True),
        ([("run_id", "r"), ("top_k", "3"), ("show_expired", "false")], True),
        ([], False),
        ([("user_id", "")], False),
        ([("user_id", "*")], False),
        ([("user_id", "a"), ("user_id", "a")], False),
        ([("user_id", "a"), ("x", "1")], False),
        ([("top_k", "3")], False),
    ],
)
def test_scope_error_query(params, ok):
    assert (scope_error_query(params) is None) is ok


@pytest.mark.parametrize(
    "body,ok",
    [
        ({"query": "q", "filters": {"user_id": "a"}}, True),
        ({"query": "q", "user_id": "a"}, True),
        ({"query": "q", "user_id": "a", "filters": {"agent_id": "b"}}, True),
        ({"query": "q", "user_id": None, "filters": {"agent_id": "b"}}, True),
        ({"query": "q", "filters": None, "run_id": "r"}, True),
        ({"query": "q"}, False),
        ({"query": "q", "filters": {"user_id": "a", "AND": []}}, False),
        ({"query": "q", "filters": {"user_id": {"eq": "a"}}}, False),
        ({"query": "q", "run_id": "*"}, False),
    ],
)
def test_scope_error_body(body, ok):
    assert (scope_error_body(body) is None) is ok
