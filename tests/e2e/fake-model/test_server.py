import json
import math
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

import pytest
import server


def cosine(a, b):
    return sum(x * y for x, y in zip(a, b))


def test_embed_is_deterministic_and_normalized():
    a = server.embed("alice likes green tea", 384)
    b = server.embed("alice likes green tea", 384)
    assert a == b
    assert len(a) == 384
    assert abs(math.sqrt(sum(x * x for x in a)) - 1) < 1e-6
    assert len(server.embed("", 16)) == 16


def test_embed_ranks_shared_words_higher():
    base = server.embed("alice likes green tea", 384)
    near = server.embed("what tea does alice like", 384)
    far = server.embed("the train leaves at noon", 384)
    assert cosine(base, near) > cosine(base, far)


PROMPT = (
    "## Summary\n\n## Last k Messages\n\n## Recently Extracted Memories\n[]\n"
    '## Existing Memories\n[{"id": "1", "text": "user: old fact"}]\n'
    "## New Messages\n%s\n## Observation Date\n2026-01-01\n## Current Date\n2026-01-01\n"
)


@pytest.mark.parametrize(
    "new_messages,want",
    [
        ("user: Alice likes green tea\n", ["Alice likes green tea"]),
        ("user: one\nassistant: ok\nuser: two\n", ["one", "two"]),
        (
            "system: be nice\nuser: multi\nline text\nassistant: hi\n",
            ["multi line text"],
        ),
        ("assistant: only assistant\n", []),
    ],
)
def test_extract_memories_reads_new_messages(new_messages, want):
    got = server.extract_memories(PROMPT % new_messages)
    assert got == want
    assert "old fact" not in " ".join(got)


@pytest.fixture
def run_server():
    servers = []

    def start(**cfg):
        httpd = server.make_server("127.0.0.1", 0, server.Config(**cfg))
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        servers.append(httpd)
        return f"http://127.0.0.1:{httpd.server_address[1]}"

    yield start
    for s in servers:
        s.shutdown()
        s.server_close()


def call(url, body=None, token=None, form=None):
    headers = {}
    data = None
    if token:
        headers["Authorization"] = "Bearer " + token
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers)
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null")


def get_token(base, secret="secret"):
    return call(
        base + "/token",
        form={
            "grant_type": "client_credentials",
            "client_id": "mem0",
            "client_secret": secret,
        },
    )


CHAT = {
    "model": "m",
    "messages": [
        {"role": "system", "content": "sys"},
        {"role": "user", "content": PROMPT % "user: Bob owns a red bike\n"},
    ],
    "response_format": {"type": "json_object"},
}


def test_token_endpoint(run_server):
    base = run_server()
    status, body = get_token(base)
    assert status == 200
    assert (
        body["token_type"] == "Bearer"
        and body["expires_in"] == 20
        and body["access_token"]
    )
    assert get_token(base, "wrong")[0] == 401
    status, _ = call(
        base + "/token",
        form={"grant_type": "password", "client_id": "mem0", "client_secret": "secret"},
    )
    assert status == 400 or status == 401


def test_chat_requires_valid_token(run_server):
    base = run_server()
    assert call(base + "/v1/chat/completions", CHAT)[0] == 401
    assert call(base + "/v1/chat/completions", CHAT, token="bogus")[0] == 401
    _, tok = get_token(base)
    status, _ = call(base + "/v1/chat/completions", CHAT, token=tok["access_token"])
    assert status == 200


def test_require_token_false_allows_anonymous(run_server):
    base = run_server(require_token=False)
    assert call(base + "/v1/chat/completions", CHAT)[0] == 200


def test_expired_token_rejected(run_server):
    base = run_server(token_ttl=1)
    _, tok = get_token(base)
    assert (
        call(base + "/v1/embeddings", {"input": "x"}, token=tok["access_token"])[0]
        == 200
    )
    time.sleep(1.2)
    assert (
        call(base + "/v1/embeddings", {"input": "x"}, token=tok["access_token"])[0]
        == 401
    )


def test_calls_log_records_token_validity(run_server):
    base = run_server()
    _, tok = get_token(base)
    call(base + "/v1/embeddings", {"input": "x"}, token=tok["access_token"])
    call(base + "/v1/embeddings", {"input": "x"}, token="bogus")
    _, calls = call(base + "/calls")
    emb = [c for c in calls if c["path"] == "/v1/embeddings"]
    assert [(c["token"], c["valid"]) for c in emb] == [
        (tok["access_token"], True),
        ("bogus", False),
    ]
    assert all("at" in c for c in calls)
    assert "secret" not in json.dumps(calls)


def test_chat_returns_memory_json(run_server):
    base = run_server()
    _, tok = get_token(base)
    status, body = call(base + "/v1/chat/completions", CHAT, token=tok["access_token"])
    assert status == 200
    assert body["object"] == "chat.completion"
    assert body["usage"]["total_tokens"] > 0
    content = json.loads(body["choices"][0]["message"]["content"])
    assert content == {"memory": [{"text": "Bob owns a red bike"}]}


@pytest.mark.parametrize("inp,n", [("one", 1), (["a b", "c d", "e f"], 3)])
def test_embeddings_shape(run_server, inp, n):
    base = run_server(dims=8)
    _, tok = get_token(base)
    status, body = call(
        base + "/v1/embeddings", {"input": inp, "model": "e"}, token=tok["access_token"]
    )
    assert status == 200
    assert body["object"] == "list" and body["model"] == "e"
    assert [d["index"] for d in body["data"]] == list(range(n))
    assert all(
        len(d["embedding"]) == 8 and d["object"] == "embedding" for d in body["data"]
    )


def test_embeddings_base64(run_server):
    import base64
    import struct

    base = run_server(dims=8)
    _, tok = get_token(base)
    _, body = call(
        base + "/v1/embeddings",
        {"input": "tea", "encoding_format": "base64"},
        token=tok["access_token"],
    )
    raw = base64.b64decode(body["data"][0]["embedding"])
    vals = struct.unpack("<8f", raw)
    assert vals == pytest.approx(server.embed("tea", 8), abs=1e-6)


def test_healthz(run_server):
    assert call(run_server() + "/healthz")[0] == 200
