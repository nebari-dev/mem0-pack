"""Pack settings: turn the pack's env vars into mem0 config overrides."""

from collections.abc import Mapping
from dataclasses import dataclass

_REQUIRED = (
    "MEM0PACK_LLM_MODEL",
    "MEM0PACK_EMBEDDING_MODEL",
    "MEM0PACK_EMBEDDING_DIMS",
    "MEM0PACK_MODEL_BASE_URL",
)


class SettingsError(ValueError):
    """Raised when the pack's environment is missing or malformed."""


@dataclass(frozen=True)
class PackSettings:
    llm_model: str
    embedding_model: str
    embedding_dims: int
    model_base_url: str
    collection: str = "memories"

    def overrides(self) -> dict:
        return {
            "llm": {
                "provider": "openai",
                "config": {"model": self.llm_model, "openai_base_url": self.model_base_url},
            },
            "embedder": {
                "provider": "openai",
                "config": {
                    "model": self.embedding_model,
                    "openai_base_url": self.model_base_url,
                },
            },
            "vector_store": {
                "provider": "pgvector",
                "config": {
                    "embedding_model_dims": self.embedding_dims,
                    "collection_name": self.collection,
                },
            },
        }


def load_settings(env: Mapping[str, str]) -> PackSettings:
    missing = [name for name in _REQUIRED if not env.get(name)]
    if missing:
        raise SettingsError("missing required settings: " + ", ".join(missing))
    try:
        dims = int(env["MEM0PACK_EMBEDDING_DIMS"])
    except ValueError:
        dims = 0
    if dims <= 0:
        raise SettingsError("MEM0PACK_EMBEDDING_DIMS must be a positive integer")
    return PackSettings(
        llm_model=env["MEM0PACK_LLM_MODEL"],
        embedding_model=env["MEM0PACK_EMBEDDING_MODEL"],
        embedding_dims=dims,
        model_base_url=env["MEM0PACK_MODEL_BASE_URL"],
        collection=env.get("MEM0PACK_COLLECTION") or "memories",
    )
