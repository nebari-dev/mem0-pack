"""ASGI middleware answering liveness and readiness probes ahead of mem0's app."""

import asyncio
from collections.abc import Callable

_OK = b'{"status":"ok"}'
_NOT_READY = b'{"status":"not ready"}'


class Health:
    """Answer GET /healthz and GET /readyz; pass everything else to the wrapped app."""

    def __init__(self, app, ready: Callable[[], bool], timeout: float = 2.0):
        self.app = app
        self.ready = ready
        self.timeout = timeout

    async def __call__(self, scope, receive, send):
        if scope["type"] == "http" and scope["method"] == "GET" and scope["path"] in ("/healthz", "/readyz"):
            if scope["path"] == "/healthz" or await self._is_ready():
                await self._respond(send, 200, _OK)
            else:
                await self._respond(send, 503, _NOT_READY)
            return
        await self.app(scope, receive, send)

    async def _is_ready(self) -> bool:
        """Run the blocking ready check off the event loop; a timeout or error means not ready."""
        try:
            return bool(await asyncio.wait_for(asyncio.to_thread(self.ready), self.timeout))
        except Exception:
            return False

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
