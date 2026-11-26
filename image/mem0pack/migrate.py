"""Init-container entrypoint: run mem0's migrations, then write mem0's config row."""

import json
import logging
import os
import subprocess
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

import sqlalchemy as sa
from sqlalchemy.orm import Session

from mem0pack.settings import SettingsError, load_settings

OVERRIDES_KEY = "config_overrides"
SERVER_DIR = Path("/app/server")

log = logging.getLogger("mem0pack.migrate")


def run_migrations(server_dir: Path, runner: Callable[..., Any] = subprocess.run) -> None:
    runner(["alembic", "upgrade", "head"], cwd=server_dir, check=True)


def write_overrides(session_factory: Callable[[], Session], overrides: dict) -> None:
    """Replace the config_overrides row. Delete then insert in one transaction.

    updated_at is set explicitly (CURRENT_TIMESTAMP, valid on SQLite and Postgres): mem0's
    column default lives in Python, not the database.
    """
    value = json.dumps(overrides, sort_keys=True, separators=(",", ":"))
    with session_factory() as session, session.begin():
        session.execute(sa.text("DELETE FROM settings WHERE key = :key"), {"key": OVERRIDES_KEY})
        session.execute(
            sa.text(
                "INSERT INTO settings (key, value, updated_at) VALUES (:key, :value, CURRENT_TIMESTAMP)"
            ),
            {"key": OVERRIDES_KEY, "value": value},
        )


def read_overrides(session_factory: Callable[[], Session]) -> dict | None:
    with session_factory() as session:
        value = session.execute(
            sa.text("SELECT value FROM settings WHERE key = :key"), {"key": OVERRIDES_KEY}
        ).scalar_one_or_none()
    if value is None:
        return None
    try:
        parsed = json.loads(value)
    except ValueError:
        return None
    return parsed if isinstance(parsed, dict) else None


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    try:
        settings = load_settings(os.environ)
    except SettingsError as exc:
        log.error("invalid pack settings: %s", exc)
        return 2

    run_migrations(SERVER_DIR)

    sys.path.insert(0, str(SERVER_DIR))
    from db import SessionLocal  # mem0's own session factory, present only in the image

    write_overrides(SessionLocal, settings.overrides())
    log.info("migrations applied and %s written", OVERRIDES_KEY)
    return 0


if __name__ == "__main__":
    sys.exit(main())
