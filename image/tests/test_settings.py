import pytest

from mem0pack.settings import PackSettings, SettingsError, load_settings

URL = "http://127.0.0.1:8081/v1"
BASE_ENV = {
    "MEM0PACK_LLM_MODEL": "chat-x",
    "MEM0PACK_EMBEDDING_MODEL": "embed-y",
    "MEM0PACK_EMBEDDING_DIMS": "384",
    "MEM0PACK_MODEL_BASE_URL": URL,
}


@pytest.mark.parametrize(
    "extra, collection",
    [({}, "memories"), ({"MEM0PACK_COLLECTION": "mem"}, "mem")],
)
def test_load_settings_reads_all_values(extra, collection):
    s = load_settings({**BASE_ENV, **extra})
    assert s.llm_model == "chat-x"
    assert s.embedding_model == "embed-y"
    assert s.embedding_dims == 384
    assert s.model_base_url == URL
    assert s.collection == collection


def test_load_settings_names_every_missing_value():
    with pytest.raises(SettingsError) as exc:
        load_settings({})
    for name in BASE_ENV:
        assert name in str(exc.value)


@pytest.mark.parametrize("dims", ["0", "-3", "abc"])
def test_load_settings_rejects_bad_dims(dims):
    with pytest.raises(SettingsError, match="MEM0PACK_EMBEDDING_DIMS"):
        load_settings({**BASE_ENV, "MEM0PACK_EMBEDDING_DIMS": dims})


def _keys(obj):
    if isinstance(obj, dict):
        for k, v in obj.items():
            yield k
            yield from _keys(v)


def test_overrides_shape():
    o = PackSettings("chat-x", "embed-y", 384, URL).overrides()
    assert o == {
        "llm": {"provider": "openai", "config": {"model": "chat-x", "openai_base_url": URL}},
        "embedder": {"provider": "openai", "config": {"model": "embed-y", "openai_base_url": URL}},
        "vector_store": {
            "provider": "pgvector",
            "config": {"embedding_model_dims": 384, "collection_name": "memories"},
        },
    }
    assert not {"api_key", "password"} & set(_keys(o))
