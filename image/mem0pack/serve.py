"""Container entrypoint: check mem0's config row, then serve mem0's FastAPI app."""

import logging
import os
import secrets
import sys
from collections.abc import Callable, Mapping, MutableMapping
from pathlib import Path
from types import ModuleType

import sqlalchemy as sa

from mem0pack.health import Health
from mem0pack.migrate import SERVER_DIR, read_overrides
from mem0pack.settings import SettingsError, load_settings

log = logging.getLogger("mem0pack.serve")


class StartupError(RuntimeError):
    pass


def ensure_env(
    env: MutableMapping[str, str], token: Callable[[int], str] = secrets.token_urlsafe
) -> None:
    """Fill in env mem0 expects: a throwaway JWT secret (auth is API-key only) and no telemetry."""
    if "JWT_SECRET" not in env:
        env["JWT_SECRET"] = token(48)
    if "MEM0_TELEMETRY" not in env:
        env["MEM0_TELEMETRY"] = "false"


def ensure_dirs(env: Mapping[str, str]) -> None:
    """Create the directories of mem0's writable files: mem0 opens them but creates none of them,
    and the volume mounted at /var/lib/mem0 starts empty."""
    for name in ("HISTORY_DB_PATH", "MEM0_TELEMETRY_STATE_PATH"):
        if env.get(name):
            Path(env[name]).parent.mkdir(parents=True, exist_ok=True)


def _first_diff(actual: dict, expected: dict, prefix: str = "") -> str | None:
    for key in sorted(set(actual) | set(expected)):
        path = f"{prefix}{key}"
        if key not in actual or key not in expected:
            return path
        a, e = actual[key], expected[key]
        if isinstance(a, dict) and isinstance(e, dict):
            nested = _first_diff(a, e, f"{path}.")
            if nested is not None:
                return nested
        elif a != e:
            return path
    return None


def check_overrides(actual: dict | None, expected: dict) -> None:
    if actual is None:
        raise StartupError("config_overrides is missing: run the migrate init container first")
    path = _first_diff(actual, expected)
    if path is not None:
        raise StartupError(f"config_overrides differs from pack settings at {path}")


def build_app(server_main: ModuleType):
    def ready() -> bool:
        try:
            with server_main.SessionLocal() as session:
                session.execute(sa.text("SELECT 1"))
            return True
        except Exception:
            return False

    return Health(server_main.app, ready=ready)


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    ensure_env(os.environ)
    ensure_dirs(os.environ)
    sys.path.insert(0, str(SERVER_DIR))
    import main as server_main  # mem0's server module, present only in the image

    try:
        check_overrides(
            read_overrides(server_main.SessionLocal), load_settings(os.environ).overrides()
        )
    except (StartupError, SettingsError) as exc:
        log.error("%s", exc)
        sys.exit(1)

    import uvicorn

    uvicorn.run(
        build_app(server_main),
        host="0.0.0.0",
        port=8000,
        workers=1,
        log_level="info",
        proxy_headers=False,
    )


if __name__ == "__main__":
    main()
