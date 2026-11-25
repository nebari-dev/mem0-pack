import json
from pathlib import Path

import pytest
import sqlalchemy as sa
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from mem0pack import migrate

# Mirrors the settings table from mem0's migration 003_create_settings.py (pinned commit).
_metadata = sa.MetaData()
_settings = sa.Table(
    "settings",
    _metadata,
    sa.Column("key", sa.String(255), primary_key=True),
    sa.Column("value", sa.Text(), nullable=False),
    sa.Column(
        "updated_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()
    ),
)


@pytest.fixture
def session_factory():
    engine = sa.create_engine(
        "sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool
    )
    _metadata.create_all(engine)
    return sessionmaker(bind=engine)


def _rows(session_factory):
    with session_factory() as session:
        return session.execute(sa.text("SELECT key, value, updated_at FROM settings")).all()


def test_write_overrides_inserts_then_replaces(session_factory):
    migrate.write_overrides(session_factory, {"a": 1})
    migrate.write_overrides(session_factory, {"b": 2})

    rows = _rows(session_factory)
    assert len(rows) == 1
    assert rows[0].key == "config_overrides"
    assert json.loads(rows[0].value) == {"b": 2}
    assert rows[0].updated_at is not None


def test_write_overrides_is_stable_json(session_factory):
    overrides = {"z": {"b": 1, "a": [1, 2]}, "a": "x"}
    migrate.write_overrides(session_factory, overrides)

    assert _rows(session_factory)[0].value == json.dumps(
        overrides, sort_keys=True, separators=(",", ":")
    )


def test_write_overrides_preserves_other_rows(session_factory):
    with session_factory() as session:
        session.execute(
            sa.text(
                "INSERT INTO settings (key, value, updated_at) VALUES ('other', 'v', CURRENT_TIMESTAMP)"
            ),
            {},
        )
        session.commit()

    migrate.write_overrides(session_factory, {"a": 1})

    assert sorted(r.key for r in _rows(session_factory)) == ["config_overrides", "other"]


@pytest.mark.parametrize(
    "stored, expected",
    [
        (None, None),
        ("not json", None),
        ('{"a":1}', {"a": 1}),
    ],
)
def test_read_overrides(session_factory, stored, expected):
    if stored is not None:
        with session_factory() as session:
            session.execute(
                sa.text(
                    "INSERT INTO settings (key, value, updated_at) "
                    "VALUES ('config_overrides', :v, CURRENT_TIMESTAMP)"
                ),
                {"v": stored},
            )
            session.commit()

    assert migrate.read_overrides(session_factory) == expected


def test_run_migrations_calls_alembic_in_server_dir():
    calls = []

    def fake_runner(*args, **kwargs):
        calls.append((args, kwargs))

    migrate.run_migrations(Path("/srv/server"), runner=fake_runner)

    assert calls == [
        ((["alembic", "upgrade", "head"],), {"cwd": Path("/srv/server"), "check": True})
    ]
