from types import SimpleNamespace

import pytest
from starlette.responses import PlainTextResponse
from starlette.testclient import TestClient

from mem0pack.serve import StartupError, build_app, check_overrides, ensure_dirs, ensure_env


def test_ensure_env_sets_missing_jwt_secret_and_telemetry():
    env: dict[str, str] = {}
    sizes = []

    def token(n):
        sizes.append(n)
        return "tok"

    ensure_env(env, token)
    assert env == {"JWT_SECRET": "tok", "MEM0_TELEMETRY": "false"}
    assert sizes == [48]


def test_ensure_env_leaves_existing_values():
    env = {"JWT_SECRET": "mine", "MEM0_TELEMETRY": "true"}
    ensure_env(env, lambda n: "other")
    assert env == {"JWT_SECRET": "mine", "MEM0_TELEMETRY": "true"}


def test_ensure_dirs_creates_parents_of_writable_files(tmp_path):
    env = {
        "HISTORY_DB_PATH": str(tmp_path / "history" / "history.db"),
        "MEM0_TELEMETRY_STATE_PATH": str(tmp_path / "state" / "telemetry.json"),
    }
    ensure_dirs(env)
    ensure_dirs(env)  # idempotent
    assert (tmp_path / "history").is_dir()
    assert (tmp_path / "state").is_dir()


def test_ensure_dirs_ignores_unset_paths():
    ensure_dirs({})


EXPECTED = {"llm": {"provider": "openai", "config": {"openai_base_url": "http://a"}}, "x": 1}


@pytest.mark.parametrize(
    "actual,message",
    [
        (None, "config_overrides is missing"),
        (
            {"llm": {"provider": "openai", "config": {"openai_base_url": "http://b"}}, "x": 1},
            "llm.config.openai_base_url",
        ),
        ({"llm": EXPECTED["llm"]}, "x"),
        ({**EXPECTED, "extra": 2}, "extra"),
    ],
)
def test_check_overrides_raises(actual, message):
    with pytest.raises(StartupError, match=message.replace(".", r"\.")):
        check_overrides(actual, EXPECTED)


def test_check_overrides_equal_passes():
    check_overrides(dict(EXPECTED), EXPECTED)


class _Session:
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def execute(self, *_):
        return None


def test_build_app_orders_health_then_allowlist():
    reached = []

    async def fake_app(scope, receive, send):
        reached.append(scope["path"])
        await PlainTextResponse("inner")(scope, receive, send)

    client = TestClient(build_app(SimpleNamespace(app=fake_app, SessionLocal=_Session)))

    assert client.get("/healthz").status_code == 200
    assert reached == []

    resp = client.post("/configure")
    assert resp.status_code == 404
    assert resp.json() == {"detail": "Not Found"}
    assert reached == []

    resp = client.get("/memories", params={"user_id": "a"})
    assert resp.status_code == 200
    assert resp.text == "inner"
    assert reached == ["/memories"]
