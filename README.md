# mem0 Pack

A Nebari software pack that runs [mem0](https://github.com/mem0ai/mem0), an open-source memory
layer for AI agents. It gives each person a memory that persists across agents, sessions and
models: preferences they've stated, and context about the work an agent is doing for them.

Declared maturity level: **Experimental** (`level: experimental` in `pack-metadata.yaml`). No
chart version is published yet (#12), so you install from a checkout of this repository.

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
- A NetworkPolicy that admits only the pods you list in `callers` to mem0, and only mem0 to its
  database. Optional egress rules close the server pod's outbound traffic.

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
endpoint, model names, embedding dimension, Keycloak token URL and Secret name. Then, from a
checkout of this repository, with both image tags set (see below):

```sh
helm install mem0 ./chart -n mem0 -f my-values.yaml \
  --set image.tag=sha-<commit> --set tools.image.tag=sha-<commit>
```

A checkout has no pinned image tags, so from a checkout you must set `image.tag` and
`tools.image.tag` to images CI pushed for that commit (`sha-<commit>`). Once #12 publishes the
chart, the release pins them and you leave them unset. `examples/argocd-app.yaml` is the same
install as an Argo CD Application.

Check it came up:

```sh
kubectl -n mem0 rollout status deploy/mem0-nebari-mem0
```

Helm prints the in-cluster URL when the install finishes. If the release name contains
`nebari-mem0`, the Service and Deployment are named after the release instead.

## Values

`callers` and the `models` block are required; the chart fails to render without them. Defaults
are in `chart/values.yaml` and the shape is enforced by `chart/values.schema.json`.

| Key | Default | Meaning |
|---|---|---|
| `image.repository`, `image.tag`, `image.digest`, `image.pullPolicy` | `quay.io/nebari/mem0-pack-server`, empty, empty, `IfNotPresent` | mem0 server image. A digest wins over a tag. One of them is required. |
| `tools.image.repository`, `.tag`, `.digest`, `.pullPolicy` | `quay.io/nebari/mem0-pack-tools`, empty, empty, `IfNotPresent` | Image for the token proxy and the secret generator. |
| `adminKey.secretName` | `mem0-admin-key` | Name of the admin key Secret (key `api-key`). The chart creates it unless `existingSecret` is set. |
| `adminKey.existingSecret` | empty | Use this Secret instead of generating one. |
| `callers` | `[]` (required) | Services allowed to reach mem0 and to read the admin key Secret. Each entry has `namespace`, `serviceAccount` and `podSelector`. At least one is required. |
| `models.baseURL` | `https://llm-internal.example.com/v1` | Internal model endpoint, with the `/v1` prefix. |
| `models.llm` | `Qwen/Qwen3.5-35B-A3B-GPTQ-Int4` | Chat model name. |
| `models.embedding.model` | empty (required) | Embedding model name. |
| `models.embedding.dimensions` | `null` (required) | Output width of the embedding model. The vector column is created with this width at first install. |
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
- **Address.** Service `<fullname>` on port 8000, for example
  `http://mem0-nebari-mem0.mem0.svc:8000`. Only pods matching a `callers` entry can connect.
- **Endpoints.** `POST /memories` (add), `GET /memories` (list), `GET /memories/{id}`,
  `DELETE /memories/{id}` and `POST /search`. `/healthz` and `/readyz` need no key.
- **Scope rule.** Every add, list and search must carry a literal, non-empty `user_id`, not `*`.
  `agent_id` and `run_id` only narrow it. Search takes no `AND`, `OR`, `NOT` or operator filters.
  An add takes a top-level `user_id` and rejects a `filters` key.
- **Errors.** `400` for an unscoped add, list or search. `401` without a valid key. `404` for
  anything not on the list, including `/auth/*`, `/configure` and `/reset`.
- **By-id calls.** `GET /memories/{id}` answers `200` with `null` for an id that does not exist.
  mem0 does not check ownership on get or delete by id, so the caller must confirm the memory
  belongs to the person before acting on it.

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
