# Contracts

Interfaces this pack promises to others, or depends on from others. One row per contract. Changing a contract updates this file in the same PR.

Status is `new`, `changed`, `unchanged` or `unchanged (implicit)` (a contract nobody wrote down, pinned by a version or commit).

| Contract | Status | Owner | Consumers | Verified by | Notes |
|---|---|---|---|---|---|
| Image names and values tag paths | new | mem0-pack | release.yaml `tag-paths` | n/a (new) | `quay.io/nebari/mem0-pack-server` and `-tools`; values `image.tag`, `tools.image.tag`. The server image's entrypoints are `python -m mem0pack.serve` (default) and `python -m mem0pack.migrate`; labels `io.nebari.mem0.commit` and `io.nebari.mem0.version` name the mem0 build. |
