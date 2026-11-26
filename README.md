# mem0 Pack

A Nebari software pack that runs [mem0](https://github.com/mem0ai/mem0), an open-source memory
layer for AI agents. It gives each person a memory that persists across agents, sessions and
models: preferences they've stated, and context about the work an agent is doing for them.

Declared maturity level: **Experimental** (`level: experimental` in `pack-metadata.yaml`). The
chart is published to the [Nebari Helm repository](https://nebari-dev.github.io/helm-repository/)
as `nebari-mem0`, starting at the prerelease `0.1.0-alpha.1` (current: `0.1.0-alpha.2`).

## Who it's for

Platform teams running AI agents on Nebari who want agents to remember things about the people
they work for, without that memory living with a model vendor. The first consumer is
[Tapas](https://github.com/nebari-dev/tapas), which calls mem0 on behalf of its agents and
enforces who can read which memories.

## What it deploys

- The mem0 server (version 2.2.1) behind a path allowlist. Only the endpoints Tapas uses are
  reachable; registration, configuration and reset are refused even with the key.
- A token proxy in the mem0 pod. It fetches short-lived Keycloak tokens and adds them to mem0's
  calls to the model server. The Keycloak client secret is mounted only into the proxy, never into
  the mem0 container.
- Postgres with pgvector for the memories, as a CloudNativePG cluster (or your own Postgres, see
  `postgres.external`).
- An admin API key in a Kubernetes Secret, created once by a Job. A Role lets the ServiceAccounts
  you list in `callers` read that one Secret.
- A NetworkPolicy that admits only the pods you list in `callers` to mem0. With the bundled
  database, a second policy admits to it only mem0's pods, the CloudNativePG operator namespace
  and the cluster's own instances. Optional egress rules close the server pod's outbound traffic.

mem0 has no per-user access control of its own: anything holding the API key can read any
person's memories. The pack keeps the key and the network path closed, and the calling service
is responsible for scoping each request to the right person.

## Prerequisites

- A Nebari cluster whose CNI enforces NetworkPolicy.
- CloudNativePG, which Nebari Infrastructure Core installs. Not needed if you use external
  Postgres; that database must already exist, be owned by the user in the Secret, and have the
  `vector` extension installed.
- llm-serving-pack serving, on its internal endpoint, a chat model that honours
  `response_format` `json_object`, and an embedding model. You set the embedding model's output
  dimension in values. It is fixed at first install.
- A Keycloak client for mem0, which the pack does not create. It must be:
  - a confidential client with service accounts enabled and standard flow off;
  - configured with a groups mapper, so its tokens carry group membership;
  - a member, through its service account, of the group or groups allowed to use the chat and
    embedding models;
  - issuing tokens whose audience includes the one the llm-serving-pack internal endpoint
    expects (its `LLM_OIDC_AUDIENCE`).

  Put its client id and secret in a Secret in the release namespace with the keys `client-id`
  and `client-secret`:

  ```sh
  kubectl create namespace mem0
  kubectl -n mem0 create secret generic mem0-model-client \
    --from-literal=client-id=<client id> --from-literal=client-secret=<client secret>
  ```

## Install

Copy `examples/nebari-values.yaml` to `my-values.yaml` and change the placeholders: model
endpoint, model names, embedding dimension, Keycloak token URL and Secret name. Then install the
published chart:

```sh
helm repo add nebari https://raw.githubusercontent.com/nebari-dev/helm-repository/gh-pages/
helm repo update nebari
helm install mem0 nebari/nebari-mem0 --version 0.1.0-alpha.2 -n mem0 -f my-values.yaml
```

or, without adding a repository, from the OCI registry:

```sh
helm install mem0 oci://quay.io/nebari/charts/nebari-mem0 --version 0.1.0-alpha.2 \
  -n mem0 -f my-values.yaml
```

The published chart pins both image tags to its release commit, so leave `image.tag` and
`tools.image.tag` unset. A prerelease is only installed when you name its version.
`examples/argocd-app.yaml` is the same install as an Argo CD Application.

To install from a checkout of this repository instead, set both image tags to images CI pushed
for that commit, because a checkout has no pinned tags:

```sh
helm install mem0 ./chart -n mem0 -f my-values.yaml \
  --set image.tag=sha-<commit> --set tools.image.tag=sha-<commit>
```

Check it came up:

```sh
kubectl -n mem0 rollout status deploy/mem0-nebari-mem0
```

Helm prints the in-cluster URL when the install finishes. If the release name contains
`nebari-mem0`, the Service and Deployment are named after the release instead.

## Values

`callers` and the `models` block are required: the chart templates fail the install with a
message naming the missing value (a bare `helm lint` still passes). Defaults are in
`chart/values.yaml` and the types and formats are enforced by `chart/values.schema.json`.

| Key | Default | Meaning |
|---|---|---|
| `image.repository`, `image.tag`, `image.digest`, `image.pullPolicy` | `quay.io/nebari/mem0-pack-server`, empty, empty, `IfNotPresent` | mem0 server image. A digest wins over a tag. One of them is required. |
| `tools.image.repository`, `.tag`, `.digest`, `.pullPolicy` | `quay.io/nebari/mem0-pack-tools`, empty, empty, `IfNotPresent` | Image for the token proxy and the secret generator. |
| `adminKey.secretName` | `mem0-admin-key` | Name of the admin key Secret (key `api-key`). The chart creates it unless `existingSecret` is set. |
| `adminKey.existingSecret` | empty | Use this Secret instead of generating one. |
| `callers` | `[]` (required) | Services allowed to reach mem0 and to read the admin key Secret. Each entry has `namespace`, `serviceAccount` and `podSelector`. At least one is required. For Tapas, `serviceAccount` is `javo-controller` with its default SQLite engine (gateway and controller share one pod) and `javo-gateway` with Postgres; the `podSelector` (`app.kubernetes.io/name: javo`, `app.kubernetes.io/component: gateway`) is the same for both. |
| `models.baseURL` | empty (required) | Internal model endpoint, with the `/v1` prefix. |
| `models.llm` | `Qwen/Qwen3.5-35B-A3B-GPTQ-Int4` | Chat model name. |
| `models.embedding.model` | empty (required) | Embedding model name. |
| `models.embedding.dimensions` | `null` (required) | Output width of the embedding model. The vector column is created with this width at first install. |
| `models.embedding.index` | `auto` | Approximate-search index on the vector column: `auto` (`hnsw` up to 2000 dimensions, `none` above), `hnsw` (refused above 2000) or `none` (exact search). See [Embedding width and the index](#embedding-width-and-the-index). |
| `models.auth.tokenURL` | empty (required) | Keycloak token endpoint. |
| `models.auth.scope` | empty | Optional scope for the token request. |
| `models.auth.existingSecret` | empty (required) | Secret with keys `client-id` and `client-secret`, created before install. |
| `postgres.bundled.enabled` | `true` | Create a CloudNativePG cluster. Set to `false` for external Postgres. |
| `postgres.bundled.instances` | `1` | Number of Postgres instances. |
| `postgres.bundled.imageName` | pinned CloudNativePG PostgreSQL 18 image | Postgres image (needs pgvector). |
| `postgres.bundled.resources` | requests 50m CPU, 256Mi memory | Postgres resources. |
| `postgres.bundled.storage.size`, `.storageClass` | `10Gi`, empty | Postgres volume size and class. |
| `postgres.bundled.operatorNamespace` | `cnpg-system` | Namespace of the CloudNativePG operator, allowed through the database NetworkPolicy. |
| `postgres.external.existingSecret` | empty | Required when `bundled.enabled` is `false`. Secret with keys `host`, `port`, `dbname`, `username`, `password`. With egress enabled, allow the database through `networkPolicy.egress.extra`. |
| `history.sizeLimit` | `1Gi` | Size limit of the scratch disk that holds mem0's history file. |
| `networkPolicy.enabled` | `true` | Render the NetworkPolicies. |
| `networkPolicy.egress.enabled` | `false` | Restrict the server pod to DNS and the bundled database. |
| `networkPolicy.egress.extra` | `[]` | Extra egress rules, rendered as given. With egress enabled they must cover the model endpoint, the token endpoint and an external database. |
| `podSecurityContext` | non-root, `fsGroup` 10001, `RuntimeDefault` seccomp | Pod security context. |
| `resources` | requests 100m CPU, 512Mi memory; limit 1Gi memory | Server container resources. |
| `toolsResources` | requests 10m CPU, 32Mi memory; limit 128Mi memory | Token proxy resources. |
| `nodeSelector`, `tolerations`, `affinity`, `podAnnotations` | empty | Standard scheduling and pod metadata. |

## Contract for callers

The full contract is in [`docs/architecture/contracts.md`](docs/architecture/contracts.md).
What a caller needs to know:

- **Admin key.** Secret `mem0-admin-key` (or `adminKey.existingSecret`) in the mem0 namespace,
  key `api-key`. The chart grants `get` on that one Secret to each ServiceAccount in `callers`.
  Send the key as the `X-API-Key` header.
  For Tapas the ServiceAccount to list is `javo-controller` when Tapas uses SQLite (its default)
  and `javo-gateway` when it uses Postgres.
- **Address.** Service `<fullname>` on port 8000, for example
  `http://mem0-nebari-mem0.mem0.svc:8000`. Only pods matching a `callers` entry can connect.
- **Endpoints.** `POST /memories` (add), `GET /memories` (list), `GET /memories/{id}`,
  `DELETE /memories/{id}` and `POST /search`. `/healthz` and `/readyz` need no key.
- **Scope rule.** Every add, list and search must carry a literal, non-empty `user_id`, not `*`, with no leading or trailing whitespace (mem0 strips it, so a padded id would reach another person's memories).
  `agent_id` and `run_id` only narrow it. Search takes no `AND`, `OR`, `NOT` or operator filters.
  An add takes a top-level `user_id` and rejects a `filters` key. `GET /memories` accepts only
  the query parameters `user_id`, `agent_id`, `run_id`, `top_k` and `show_expired`, each at most
  once; any other parameter, or a repeat, is a `400`. The `{id}` in by-id paths must be a
  lowercase UUID; anything else is a `404`.
- **Errors.** `400` for an unscoped add, list or search. `401` without a valid key. `404` for
  anything not on the list, including `/auth/*`, `/configure` and `/reset`.
- **By-id calls.** `GET /memories/{id}` answers `200` with `null` for an id that does not exist.
  mem0 does not check ownership on get or delete by id, so the caller must confirm the memory
  belongs to the person before acting on it.

## Embedding width and the index

mem0 creates a pgvector HNSW index on its vector column by default. pgvector supports HNSW on the
`vector` type only up to 2000 dimensions ([pgvector v0.8.7 README, "Supported types"](https://github.com/pgvector/pgvector/blob/v0.8.7/README.md#indexing)),
so a wider embedding makes mem0 fail to create its table. `models.embedding.index` avoids that:

- `auto` (default): `hnsw` when `dimensions` is 2000 or less, `none` above.
- `none`: no approximate index. mem0 does exact search, which is fine at the scale of one person's
  memories.
- `hnsw`: always build the index. The chart refuses it with more than 2000 dimensions (the schema
  error reads `'not' failed` at `/models/embedding/index`), and so does the server image at start.

Widths of common models, from the providers' documentation (set `dimensions` to the width you
request when the model lets you shorten it):

| Model | Default width | Index under `auto` |
| --- | --- | --- |
| OpenAI `text-embedding-3-small` | 1536 | `hnsw` |
| OpenAI `text-embedding-3-large` | 3072 | `none` |
| AWS Bedrock Titan Text Embeddings V2 | 1024 (also 512, 256) | `hnsw` |
| Cohere `embed-v4.0` | 1536 (also 1024, 512, 256) | `hnsw` |
| Cohere `embed-english-v3.0`, `embed-multilingual-v3.0` | 1024 | `hnsw` |
| Google `gemini-embedding-001` | 3072 (768, 1536 and 3072 recommended) | `none` |
| OpenRouter | depends on the model | check the model's page |

Changing `models.embedding.index` on an existing install does nothing to the database. mem0
v2.2.1 builds the HNSW index only when it creates its table (`create_col` in
`mem0/vector_stores/pgvector.py`), and it creates the table lazily on the first memory operation.
Once the table exists, a restart with another index value never reaches that code. The table's
width is fixed in the same way and cannot change after first install.

- `none` to `hnsw` does not create the index. Add it by hand, only when the width is 2000 or less:
  `CREATE INDEX IF NOT EXISTS memories_hnsw_idx ON memories USING hnsw (vector vector_cosine_ops);`
- `hnsw` to `none` leaves the existing index in place. Remove it by hand if you want it gone:
  `DROP INDEX IF EXISTS memories_hnsw_idx;`

The table and index are named after the collection, `memories` unless you changed it. Run the
statement with psql against the pack's database, the database named by the `dbname` key of the
database Secret: for the bundled database, in the CloudNativePG primary pod (the Cluster is named
`<release fullname>-db`, so the pod is `<release fullname>-db-1`):
`kubectl exec -n mem0 -it <release fullname>-db-1 -- psql -d <dbname>`; for an external database,
with your own psql connection. The pack has no migration for either direction.

## Known limitations

- mem0 has no per-person access control. Anyone with the admin key can read every person's
  memories, so callers must scope each request and check ownership on by-id calls.
- mem0's history and raw messages are kept in SQLite on a scratch disk. They are lost when the
  pod restarts. The memories themselves are in Postgres.
- One replica. Updates restart the pod.
- The embedding dimension is fixed at first install. Changing it later is not supported.
- spaCy is not installed, so there is no entity extraction and search is semantic only.
- The Keycloak client is a prerequisite the pack does not create.
- The pack has been tested only against a fake model on kind. A run against a real
  llm-serving-pack is not done yet (#3).

## Development

Unit tests and static checks (from the repository root):

```sh
helm unittest chart/                             # needs the helm-unittest plugin
bash chart/tests/values-wiring.sh                # renders examples/nebari-values.yaml
bash chart/tests/deterministic.sh                # two renders must be identical
(cd image && uv run --no-project --with-requirements requirements-dev.txt pytest -q)
(cd tools && go test ./... -race -count=1)
```

The end-to-end journeys run on a kind cluster against a fake model and a fake token endpoint.
They need docker, kind, helm, kubectl and jq:

```sh
bash tests/e2e/kind-e2e.sh             # create the cluster, run the journeys, delete it
KEEP_CLUSTER=1 bash tests/e2e/kind-e2e.sh   # same, but keep the cluster
```

`AGENTS.md` lists every command CI runs.
