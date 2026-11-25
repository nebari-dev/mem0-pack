"""Test-only fake OpenAI-compatible model with a client-credentials token endpoint.

Stdlib only. Not for production use.
"""

import base64
import hashlib
import json
import math
import os
import re
import secrets
import struct
import threading
import time
import urllib.parse
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STOP_WORDS = frozenset(
    [
        "a",
        "an",
        "and",
        "are",
        "as",
        "at",
        "be",
        "by",
        "do",
        "does",
        "did",
        "for",
        "from",
        "has",
        "have",
        "in",
        "is",
        "it",
        "of",
        "on",
        "or",
        "that",
        "the",
        "this",
        "to",
        "was",
        "were",
        "what",
        "when",
        "where",
        "which",
        "who",
        "why",
        "with",
    ]
)


def _words(text):
    return [w for w in re.findall(r"[a-z0-9]+", text.lower()) if w not in STOP_WORDS]


def embed(text, dims):
    vec = [0.0] * dims
    for w in _words(text):
        h = int.from_bytes(hashlib.sha256(w.encode()).digest()[:8], "big")
        vec[h % dims] += 1.0
    norm = math.sqrt(sum(x * x for x in vec))
    if norm == 0:
        vec[0] = 1.0
        return vec
    return [x / norm for x in vec]


_ROLE_RE = re.compile(r"^(system|user|assistant): ?(.*)$")


def extract_memories(user_prompt):
    """Return the user message texts from the `## New Messages` section of mem0's prompt.

    mem0 renders that section as lines of `role: content` (mem0/memory/utils.py parse_messages).
    A line without a role prefix continues the previous message.
    """
    m = re.search(
        r"^## New Messages\n(.*?)(?=^## Observation Date\n|\Z)",
        user_prompt,
        re.DOTALL | re.MULTILINE,
    )
    if not m:
        return []
    msgs = []  # [role, text]
    for line in m.group(1).splitlines():
        r = _ROLE_RE.match(line)
        if r:
            msgs.append([r.group(1), r.group(2)])
        elif msgs and line.strip():
            msgs[-1][1] += " " + line.strip()
    return [t.strip() for role, t in msgs if role == "user" and t.strip()]


@dataclass
class Config:
    client_id: str = "mem0"
    client_secret: str = "secret"
    token_ttl: int = 20
    dims: int = 384
    require_token: bool = True
    tokens: dict = field(default_factory=dict)
    calls: list = field(default_factory=list)
    lock: threading.Lock = field(default_factory=threading.Lock)


def config_from_env():
    return Config(
        client_id=os.environ.get("CLIENT_ID", "mem0"),
        client_secret=os.environ.get("CLIENT_SECRET", "secret"),
        token_ttl=int(os.environ.get("TOKEN_TTL", "20")),
        dims=int(os.environ.get("DIMS", "384")),
        require_token=os.environ.get("REQUIRE_TOKEN", "true").lower() != "false",
    )


def _tokens_estimate(text):
    return max(1, len(text.split()))


def make_handler(cfg):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):
            pass  # never log request details

        def _send(self, status, obj):
            body = json.dumps(obj).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _read(self):
            n = int(self.headers.get("Content-Length") or 0)
            return self.rfile.read(n) if n else b""

        def _bearer(self):
            auth = self.headers.get("Authorization", "")
            return auth[7:] if auth.startswith("Bearer ") else ""

        def _valid(self, token):
            with cfg.lock:
                exp = cfg.tokens.get(token)
            return exp is not None and time.monotonic() < exp

        def _authorize(self):
            """Record the call; return True if it may proceed."""
            token = self._bearer()
            valid = self._valid(token)
            with cfg.lock:
                cfg.calls.append(
                    {
                        "path": self.path,
                        "token": token,
                        "valid": valid,
                        "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    }
                )
            if cfg.require_token and not valid:
                self._send(
                    401,
                    {
                        "error": {
                            "message": "invalid or expired token",
                            "type": "invalid_request_error",
                        }
                    },
                )
                return False
            return True

        def do_GET(self):
            if self.path == "/healthz":
                self._send(200, {"status": "ok"})
            elif self.path == "/calls":
                with cfg.lock:
                    self._send(200, list(cfg.calls))
            else:
                self._send(404, {"error": "not found"})

        def do_POST(self):
            raw = self._read()
            if self.path == "/token":
                return self._token(raw)
            if self.path not in ("/v1/chat/completions", "/v1/embeddings"):
                return self._send(404, {"error": "not found"})
            if not self._authorize():
                return
            try:
                req = json.loads(raw or b"{}")
            except ValueError:
                return self._send(400, {"error": {"message": "invalid json"}})
            if self.path == "/v1/embeddings":
                self._embeddings(req)
            else:
                self._chat(req)

        def _token(self, raw):
            form = {k: v[0] for k, v in urllib.parse.parse_qs(raw.decode()).items()}
            if form.get("grant_type") != "client_credentials":
                return self._send(400, {"error": "unsupported_grant_type"})
            ok = secrets.compare_digest(
                form.get("client_id", ""), cfg.client_id
            ) & secrets.compare_digest(form.get("client_secret", ""), cfg.client_secret)
            if not ok:
                return self._send(401, {"error": "invalid_client"})
            token = secrets.token_urlsafe(24)
            with cfg.lock:
                cfg.tokens[token] = time.monotonic() + cfg.token_ttl
            self._send(
                200,
                {
                    "access_token": token,
                    "token_type": "Bearer",
                    "expires_in": cfg.token_ttl,
                },
            )

        def _embeddings(self, req):
            inp = req.get("input", "")
            texts = [inp] if isinstance(inp, str) else list(inp)
            b64 = req.get("encoding_format") == "base64"
            data = []
            for i, t in enumerate(texts):
                v = embed(t, cfg.dims)
                if b64:
                    v = base64.b64encode(struct.pack(f"<{len(v)}f", *v)).decode()
                data.append({"object": "embedding", "index": i, "embedding": v})
            n = sum(_tokens_estimate(t) for t in texts)
            self._send(
                200,
                {
                    "object": "list",
                    "data": data,
                    "model": req.get("model", "fake-embedding"),
                    "usage": {"prompt_tokens": n, "total_tokens": n},
                },
            )

        def _chat(self, req):
            msgs = req.get("messages") or []
            user = ""
            for m in reversed(msgs):
                if m.get("role") == "user":
                    c = m.get("content")
                    user = c if isinstance(c, str) else json.dumps(c)
                    break
            content = json.dumps(
                {"memory": [{"text": t} for t in extract_memories(user)]}
            )
            p = sum(_tokens_estimate(str(m.get("content", ""))) for m in msgs)
            c = _tokens_estimate(content)
            self._send(
                200,
                {
                    "id": "chatcmpl-" + secrets.token_hex(8),
                    "object": "chat.completion",
                    "created": int(time.time()),
                    "model": req.get("model", "fake-chat"),
                    "choices": [
                        {
                            "index": 0,
                            "message": {"role": "assistant", "content": content},
                            "finish_reason": "stop",
                        }
                    ],
                    "usage": {
                        "prompt_tokens": p,
                        "completion_tokens": c,
                        "total_tokens": p + c,
                    },
                },
            )

    return Handler


def make_server(host, port, cfg):
    httpd = ThreadingHTTPServer((host, port), make_handler(cfg))
    httpd.daemon_threads = True
    return httpd


if __name__ == "__main__":
    make_server(
        "0.0.0.0", int(os.environ.get("PORT", "8080")), config_from_env()
    ).serve_forever()
