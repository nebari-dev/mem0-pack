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
            "config": {
                "embedding_model_dims": 384,
                "collection_name": "memories",
                "hnsw": True,
                "diskann": False,
            },
        },
    }
    assert not {"api_key", "password"} & set(_keys(o))


@pytest.mark.parametrize(
    "index, dims, hnsw",
    [
        (None, "384", True),
        ("auto", "2000", True),
        ("auto", "2001", False),
        ("auto", "3072", False),
        ("hnsw", "2000", True),
        ("none", "384", False),
        ("none", "3072", False),
    ],
)
def test_embedding_index_resolves_to_hnsw_flag(index, dims, hnsw):
    env = {**BASE_ENV, "MEM0PACK_EMBEDDING_DIMS": dims}
    if index is not None:
        env["MEM0PACK_EMBEDDING_INDEX"] = index
    cfg = load_settings(env).overrides()["vector_store"]["config"]
    assert cfg["hnsw"] is hnsw
    assert cfg["diskann"] is False


def test_hnsw_above_2000_dims_is_refused():
    env = {
        **BASE_ENV,
        "MEM0PACK_EMBEDDING_DIMS": "2048",
        "MEM0PACK_EMBEDDING_INDEX": "hnsw",
    }
    with pytest.raises(SettingsError) as exc:
        load_settings(env)
    msg = str(exc.value)
    assert "MEM0PACK_EMBEDDING_INDEX" in msg
    assert "MEM0PACK_EMBEDDING_DIMS" in msg
    assert "none" in msg and "auto" in msg


@pytest.mark.parametrize("index", ["ivfflat", "HNSW", "true"])
def test_unknown_embedding_index_is_refused(index):
    with pytest.raises(SettingsError, match="MEM0PACK_EMBEDDING_INDEX"):
        load_settings({**BASE_ENV, "MEM0PACK_EMBEDDING_INDEX": index})


def test_empty_embedding_index_means_auto():
    s = load_settings({**BASE_ENV, "MEM0PACK_EMBEDDING_INDEX": ""})
    assert s.embedding_index == "auto"
