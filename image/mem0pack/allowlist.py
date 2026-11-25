"""ASGI middleware admitting only the mem0 endpoints Tapas uses, each scoped to a person.

mem0 has no per-user access control, so this is the pack's main security control: anything
not listed in RULES is a 404, and list and search calls must name a literal user, agent or run.
"""

import json
import re
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Literal
from urllib.parse import parse_qsl

MAX_BODY = 1024 * 1024
SCOPE_KEYS = ("user_id", "agent_id", "run_id")
_QUERY_KEYS = frozenset((*SCOPE_KEYS, "top_k", "show_expired"))
_UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
_NOT_FOUND = b'{"detail":"Not Found"}'


@dataclass(frozen=True)
class Rule:
    method: str
    pattern: re.Pattern[str]
    scope: Literal["none", "query", "body"]


RULES: tuple[Rule, ...] = (
    Rule("POST", re.compile(r"/memories\Z"), "none"),
    Rule("GET", re.compile(r"/memories\Z"), "query"),
    Rule("GET", re.compile(rf"/memories/{_UUID}\Z"), "none"),
    Rule("DELETE", re.compile(rf"/memories/{_UUID}\Z"), "none"),
    Rule("POST", re.compile(r"/search\Z"), "body"),
)


def _value_error(key: str, value) -> str | None:
    """A scope value must be a literal non-empty string and not the wildcard."""
    if not isinstance(value, str) or value == "":
        return f"{key} must be a non-empty string"
    if value == "*":
        return f"{key} must not be a wildcard"
    return None


def scope_error_query(params: list[tuple[str, str]]) -> str | None:
    """Return why a GET /memories query is not scoped, or None when it is."""
    seen: set[str] = set()
    for key, value in params:
        if key not in _QUERY_KEYS:
            return f"query parameter {key!r} is not allowed"
        if key in seen:
            return f"query parameter {key!r} may appear only once"
        seen.add(key)
        if key in SCOPE_KEYS and (err := _value_error(key, value)):
            return err
    if not seen.intersection(SCOPE_KEYS):
        return "one of user_id, agent_id or run_id is required"
    return None


def scope_error_body(body: dict) -> str | None:
    """Return why a POST /search body is not scoped, or None when it is.

    Top-level user_id/agent_id/run_id (deprecated in mem0) and `filters` follow the same
    rules; null counts as absent, as it does in mem0.
    """
    present: set[str] = set()
    for key in SCOPE_KEYS:
        value = body.get(key)
        if value is None:
            continue
        if err := _value_error(key, value):
            return err
        present.add(key)
    filters = body.get("filters")
    if filters is not None:
        if not isinstance(filters, dict):
            return "filters must be an object"
        for key, value in filters.items():
            if key not in SCOPE_KEYS:
                return f"filter {key!r} is not allowed; only user_id, agent_id and run_id"
            if err := _value_error(f"filters.{key}", value):
                return err
            present.add(key)
    if not present:
        return "one of user_id, agent_id or run_id is required"
    return None


def _reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict:
    out: dict = {}
    for key, value in pairs:
        if key in out:
            raise ValueError(f"duplicate key {key!r}")
        out[key] = value
    return out


class _TooLarge(Exception):
    pass


class Allowlist:
    """Pass only RULES through to the wrapped app; 404 the rest, 400 unscoped list and search."""

    def __init__(self, app, rules: Sequence[Rule] = RULES):
        self.app = app
        self.rules = tuple(rules)

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        rule = self._match(scope)
        if rule is None:
            await self._respond(send, 404, _NOT_FOUND)
            return
        if rule.scope == "query":
            params = parse_qsl(scope.get("query_string", b"").decode("latin-1"), True)
            if err := scope_error_query(params):
                await self._bad_request(send, err)
                return
        try:
            body = await self._read_body(receive)
        except _TooLarge:
            await self._bad_request(send, "request body too large")
            return
        if rule.scope == "body" and (err := self._body_error(body)):
            await self._bad_request(send, err)
            return
        await self.app(scope, self._replay(body), send)

    def _match(self, scope) -> Rule | None:
        path = scope["path"]
        raw = scope.get("raw_path")
        # Any percent-encoding, or a decoded path that differs from the wire path, could
        # make this match disagree with the router's, so none of it is allowed.
        if raw is not None and (b"%" in raw or raw != path.encode("utf-8")):
            return None
        for rule in self.rules:
            if rule.method == scope["method"] and rule.pattern.fullmatch(path):
                return rule
        return None

    @staticmethod
    def _body_error(body: bytes) -> str | None:
        try:
            parsed = json.loads(body, object_pairs_hook=_reject_duplicate_keys)
        except (ValueError, RecursionError):
            return "request body must be valid JSON"
        if not isinstance(parsed, dict):
            return "request body must be a JSON object"
        return scope_error_body(parsed)

    @staticmethod
    async def _read_body(receive) -> bytes:
        chunks: list[bytes] = []
        size = 0
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                break
            chunk = message.get("body", b"")
            size += len(chunk)
            if size > MAX_BODY:
                raise _TooLarge
            chunks.append(chunk)
            if not message.get("more_body", False):
                break
        return b"".join(chunks)

    @staticmethod
    def _replay(body: bytes):
        sent = False

        async def receive():
            nonlocal sent
            if not sent:
                sent = True
                return {"type": "http.request", "body": body, "more_body": False}
            return {"type": "http.disconnect"}

        return receive

    async def _bad_request(self, send, message: str) -> None:
        await self._respond(send, 400, json.dumps({"detail": message}).encode())

    @staticmethod
    async def _respond(send, status: int, body: bytes) -> None:
        await send(
            {
                "type": "http.response.start",
                "status": status,
                "headers": [
                    (b"content-type", b"application/json"),
                    (b"content-length", str(len(body)).encode()),
                ],
            }
        )
        await send({"type": "http.response.body", "body": body})
