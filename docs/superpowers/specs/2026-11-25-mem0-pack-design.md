# mem0 pack: design

**Date:** 2026-11-25
**Status:** approved for build (decisions made with the owner in chat on 2026-11-25; the rest delegated to the builder and marked "builder's call")
**Repos:** `nebari-dev/mem0-pack` (primary, this repo). Touches `nebari-dev/tapas` only through the contract in nebari-dev/tapas#15.
**Issues covered:** mem0-pack #2, #4, #5, #6, #7, #8, #9, #11, #13 (and the template cleanup they imply). Out of scope: #3's live run on a real llm-serving-pack, #10, #12, #14, #15.
**Source design:** Tapas "Policy and memory proxy for agents" (`docs/superpowers/specs/2026-11-24-policy-memory-proxy-design.md` in the tapas checkout, Memory section).

## Intent

Tapas gives each person a memory that follows them across agents and models. The memory lives in mem0, run by this pack on the company's own cluster. Tapas is the only caller: it holds the only key and scopes every request to one person (`user_id` = the person, `agent_id` = the agent app).

mem0 v2.2.1 has no per-person access control, and several of its endpoints can take over or wipe the whole store. So the pack's job is not just "run mem0". It runs mem0 with everything closed that Tapas doesn't need, with the key and the network path limited to Tapas, and with its own model calls going to llm-serving-pack under a service identity of its own.

## Goals

- `helm install` on a Nebari cluster with CloudNativePG brings up mem0 with Postgres and pgvector, with no Secret made by hand in mem0's namespace.
- Tapas reads mem0's admin key from mem0's namespace through an RBAC grant, and can add, search, list, get and delete memories for one person.
- mem0's extraction and embedding calls go to llm-serving-pack's internal endpoint with a short-lived Keycloak token from a service-account client. The client secret never enters mem0's container.
- Only the paths Tapas uses are reachable. Registration, configuration, reset, key management, entities, request logs, docs and history are refused even with the key.
- Only named callers can reach mem0, and only mem0 (and the CloudNativePG operator) can reach its Postgres.
- Nothing calls out except to the model, Keycloak and Postgres. Telemetry is off.
- Renders are deterministic, so ArgoCD never sees drift.
- CI proves all of the above on kind against a fake model on every PR.

## Non-goals

- A live run against a real llm-serving-pack and a real embedding model (#3's live half). That waits for the new cluster; everything here is proven against a fake model.
- Installing on any real cluster (#10), cutting a release (#12), backups (#14), promotion (#15).
- The mem0 dashboard. It is a separate service and we don't ship it.
- Keeping mem0's history and raw-message store in Postgres. It stays in SQLite on scratch disk for now (decided); a Postgres store with deletion rules is future work.
- A docs site. Not required until GA; `docs_site: false`.

## Anti-goals (a failure even if it works)

- Any path by which a pod other than a named caller reaches mem0, or anything but mem0 reaches its Postgres.
- `POST /auth/register`, `POST /configure` or `POST /reset` reachable by anyone, key or not.
- mem0's container holding the Keycloak client secret, or the token proxy being reachable from outside its pod (it would hand out authenticated model calls to anyone).
- A render that differs between two runs (ArgoCD drift, a rotated key).
- One person's memories returned for another person's `user_id`.
- Any outbound call to PostHog, GitHub or another third-party host at runtime.

## Constraints

- mem0 server and library pinned to tag v2.2.1 (commit `94c3fe9f238f3dbf29c9ce98643bd71eb13077cd`, `mem0ai==2.2.1`).
- mem0's own files are not modified (decided: allowlist, not patching). Our code wraps and configures them from outside.
- Nebari conventions: NIC installs CloudNativePG 1.30 (chart 0.29.0); images publish through `nebari-dev/.github` reusable workflows; `pack-metadata.yaml` validates against the dashboard schema.
- GitOps-safe rendering: no `lookup`, `randAlphaNum`, `genCA` or `uuidv4` in templates. Secrets are made in the cluster by a Job, or supplied.
- Release-readiness checklist: images pinned to a tag or digest; non-root, read-only root filesystem, no privilege escalation; NetworkPolicy.
- Testing on kind only, with a fake OpenAI-compatible model and a fake token endpoint (decided).
- No AI attribution and no em dashes in anything committed.

## Approaches

Decided with the owner:

| Question | Options considered | Decision |
|---|---|---|
| How mem0 authenticates to llm-serving-pack | A: key-manager API keys on the external endpoint (two per-model keys, never expire). B: Keycloak service-account JWT on the internal endpoint through a token-injecting sidecar. C: patch mem0 to refresh tokens. | **B**. Access follows Keycloak in real time, revocation works, matches the Tapas spec and how Tapas calls the same service. |
| How dangerous endpoints are closed | Path allowlist in front of the server; patching mem0's routes | **Allowlist**. |
| Where the admin key comes from | Helm `lookup` (rejected: churns under ArgoCD); hand-made in two namespaces; ensure-secret Job plus an RBAC grant to Tapas's ServiceAccount | **Job + RBAC grant**; Tapas side is nebari-dev/tapas#15. |
| Where mem0's SQLite history/messages file lives | Persistent volume; scratch disk; our own Postgres store | **Scratch disk** for now; Postgres later for scale and durability. |
| Test environment | Fake model on kind; a real cluster | **Fake model on kind**. |

Builder's calls (delegated):

| Question | Options considered | Call and reason |
|---|---|---|
| Allowlist mechanism | ASGI wrapper in our own entrypoint; nginx or Envoy sidecar | **ASGI wrapper.** We need our own entrypoint anyway (health, `JWT_SECRET`). One process, no second config language, no bypass because the wrapper is the outermost app on the only listening port. |
| Token proxy | Our own Go reverse proxy; Envoy credential injector; a thread inside mem0's Python process | **Our own Go proxy**, in a separate container. Keeps the client secret out of mem0's container. The Envoy route was not verified for client credentials at a version we would pin. |
| How the ensure-secret Job and token proxy ship | Python script in the mem0 image; one Go binary with subcommands | **One Go binary, `mem0-pack-tools`**, subcommands `token-proxy` and `ensure-secret`, one small distroless image. Mirrors llm-serving-pack's `openmeter-setup ensure-secret`. |
| How chart values reach mem0's config | Monkeypatch `server_state.initialize_state`; write mem0's own `config_overrides` settings row before the server starts | **Write the `config_overrides` row** from the migration init container. It is mem0's own persistence mechanism (what `/configure` writes), read at import (`server/server_state.py:20-35,76-83`). Rewriting it on every start also wipes any override left by an earlier `/configure`. |
| Fetching mem0's source | Vendor `server/` into this repo; clone at the pinned commit in a build stage | **Clone at the commit** in a build stage and check `git rev-parse HEAD`. Nothing vendored to keep in sync. |
| Database layout | Two databases as upstream (`postgres` + `mem0_app`); one database | **One database** (`mem0`) for both vectors and app tables. Table names don't collide (`memories` vs `users`, `api_keys`, `request_logs`, `settings`, `refresh_token_jtis`, `alembic_version`). Falsify: the e2e journey runs migrations and add/search against one database. |
| spaCy (entity extraction, BM25 lemmatization) | Leave it out, as upstream's server image does; bake spaCy and `en_core_web_sm` into the image | **Leave it out.** With spaCy installed but no model, mem0 downloads one at runtime (`mem0/utils/spacy_models.py:21-41`), which breaks the no-outbound rule and a read-only root fs. Without it, entity extraction returns nothing and search is semantic only (`mem0/memory/main.py:546`). Baking the model in is a follow-up if search quality needs it. |
| How strict the scope rule is | Require any `user_id`/`agent_id` key; require literal values with no operators | **Literal values only.** mem0 treats `"*"` as a wildcard and accepts `AND`/`OR`/`NOT` and operator dicts in filters (`mem0/memory/main.py:1424-1431,1634-1640`), and an empty `user_id` on `GET /memories` falls through to the admin list of everything (`server/main.py:423-428`). Tapas's client isn't written yet; strict now, widened only by a contract change. |
| Guarding against a silent config fallback | Trust the `config_overrides` row; also point the OpenAI env at the token proxy; also assert the row at start | **Both guards.** `_load_overrides` swallows every error and returns `{}` (`server/server_state.py:20-35`), after which mem0 would call `api.openai.com` with memory contents. `OPENAI_BASE_URL` in env also points at the token proxy (the library's fallback, `mem0/llms/openai.py:51`, `mem0/embeddings/openai.py:23-25`), and `serve` refuses to start unless the row equals the expected overrides. |
| Who creates mem0's Keycloak client | This pack; the cluster's GitOps (realm setup); nebari-operator | **A documented prerequisite** for now. The operator provisions clients only for a NebariApp, and mem0 has none. README states what the client needs (confidential, service account only, groups mapper, membership in the models' group, the internal endpoint's audience). #10 creates it on the new cluster. |

## Design

### Server image (`image/`, issue #2)

- Build stage: `git clone https://github.com/mem0ai/mem0` and `git checkout 94c3fe9f...`; fail unless `git rev-parse HEAD` matches. Copy `server/*.py`, `server/routers/`, `server/alembic/`, `server/alembic.ini` into `/app/server`.
- Runtime stage: `python:3.12-slim` pinned by digest. Dependencies from `image/requirements.txt`, compiled from `image/requirements.in` with `uv pip compile --generate-hashes` and installed with `--require-hashes`. `requirements.in` pins `mem0ai==2.2.1` and the server's own dependencies (`server/requirements.txt` at the tag) at exact versions, minus `anthropic` and `google-generativeai` (only the OpenAI provider is used). `posthog` stays: the library imports it unconditionally (`mem0/memory/telemetry.py:9`).
- Our package `mem0pack` in `/app/mem0pack`.
- `ENV MEM0_TELEMETRY=false PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 MEM0_DIR=/var/lib/mem0/home HISTORY_DB_PATH=/var/lib/mem0/history/history.db MEM0_TELEMETRY_STATE_PATH=/var/lib/mem0/history/telemetry.json`. Runs as UID 10001. Writable paths are only `/var/lib/mem0` and `/tmp` (both `emptyDir` in the chart), so the root filesystem is read-only.
- Labels: `org.opencontainers.image.source`, `io.nebari.mem0.commit=94c3fe9f...`, `io.nebari.mem0.version=2.2.1`.
- Entrypoints: `python -m mem0pack.serve` (default) and `python -m mem0pack.migrate`.

### `mem0pack` (Python, in the server image; issues #4, #6, #9)

- `settings.py` reads the pack's env (`MEM0PACK_LLM_MODEL`, `MEM0PACK_EMBEDDING_MODEL`, `MEM0PACK_EMBEDDING_DIMS`, `MEM0PACK_MODEL_BASE_URL`, `MEM0PACK_COLLECTION`) and builds the `config_overrides` dict: `llm.config.{model, openai_base_url}`, `embedder.config.{model, openai_base_url}`, `vector_store.config.{embedding_model_dims, collection_name}`. No secrets go in it; the Postgres password stays in env where the upstream `DEFAULT_CONFIG` reads it.
- `migrate.py` runs `alembic upgrade head` in `/app/server`, then upserts the `settings` row `config_overrides` with the dict above. Runs as an init container, every start.
- `allowlist.py` is an ASGI middleware. It admits:

  | Method | Path | Extra rule |
  |---|---|---|
  | `POST` | `/memories` | |
  | `GET` | `/memories` | scoped (below); other query params limited to `top_k`, `show_expired` |
  | `GET` | `/memories/{id}` | |
  | `DELETE` | `/memories/{id}` | |
  | `POST` | `/search` | scoped (below), in `filters` or the deprecated top-level fields |
  | `GET` | `/healthz`, `/readyz` | answered by the wrapper, no auth |

  **Scoped** means: at least one of `user_id`, `agent_id`, `run_id`; every one present is a non-empty string that is not `"*"`; `filters` contains no keys other than those three, and no `AND`/`OR`/`NOT` and no operator dicts. Anything else gets `400 {"detail":"..."}` before mem0 sees it. Everything not in the table gets `404 {"detail":"Not Found"}`, the same shape FastAPI uses. `{id}` must match a UUID.
- `health.py`: `/healthz` returns 200 if the process runs. `/readyz` runs `SELECT 1` through mem0's `SessionLocal` with a 2 s timeout.
- `serve.py`: sets `JWT_SECRET` to 48 random bytes (base64) if unset, because mem0 refuses to start without it (`server/main.py:90-94`) and nothing uses it once `/auth` is closed. Sets `MEM0_TELEMETRY=false` if unset. Imports `main`, then reads the `config_overrides` row and exits non-zero unless it equals `load_settings(env).overrides()` (so a missing or stale row can't silently fall back to OpenAI). Wraps `main.app` with health then allowlist, runs uvicorn on `0.0.0.0:8000`, one worker, no reload.

### `mem0-pack-tools` (Go, `tools/`; issues #6, #7)

- `token-proxy`: listens on `127.0.0.1:8081` only. For each request: get a token (cached; refreshed when less than 30 s or 25% of its life remains, whichever is smaller), drop the caller's `Authorization`, set `Authorization: Bearer <token>`, forward to `UPSTREAM_URL` with `httputil.ReverseProxy`. Token: `POST TOKEN_URL` with `grant_type=client_credentials`, client id and secret read from files (mounted Secret), optional `scope`. A failed token fetch answers `502` with a JSON error and logs it; it never forwards without a token. A separate listener on `0.0.0.0:8082` answers only `GET /healthz` (200 once a token has been fetched once). Runs as a native sidecar (init container with `restartPolicy: Always`) so it is up before mem0 starts.
- `ensure-secret -name N -key K`: creates Secret `N` in its own namespace with key `K` = 32 random bytes hex-encoded. `409 AlreadyExists` is success and the existing value is never read or changed. Uses the in-cluster ServiceAccount token and CA; no client-go (stdlib `net/http`), to keep the binary small.
- Logs with `log/slog` JSON; errors wrapped `fmt.Errorf("verb noun: %w", err)`.

### Chart (`chart/`, issues #4, #5, #7, #8)

Name `nebari-mem0`. Resources:

- **Deployment** `<fullname>`: replicas 1, strategy `Recreate`. Init containers: `migrate` (server image, `python -m mem0pack.migrate`), then `token-proxy` (tools image, native sidecar). Main container `server`. Env: Postgres from the CloudNativePG Secret (`username`, `password`, `host`, `port`, `dbname`) mapped to `POSTGRES_*` and `APP_DB_NAME=POSTGRES_DB`; `ADMIN_API_KEY` from the admin-key Secret; `OPENAI_API_KEY=unused-token-proxy-injects` (the OpenAI client requires a value); `MEM0PACK_MODEL_BASE_URL` and `OPENAI_BASE_URL` both `http://127.0.0.1:8081/v1`. Volumes: `emptyDir` for `/var/lib/mem0` (`sizeLimit` from values) and `/tmp`; client credentials Secret mounted only into `token-proxy`. Probes: readiness `/readyz`, liveness `/healthz`. Security context: non-root, read-only root fs, drop ALL, no privilege escalation, `seccompProfile: RuntimeDefault` (Pod Security `restricted`).
- **Service** `<fullname>` port 8000.
- **CloudNativePG `Cluster`** `<fullname>-db` when `postgres.bundled.enabled`: `instances` from values (default 1, `enablePDB: false` when 1), `imageName` pinned (`ghcr.io/cloudnative-pg/postgresql:18.4-standard-trixie` by digest; builder confirms the digest), `bootstrap.initdb` database `mem0` owner `mem0`, `postInitApplicationSQL: ["CREATE EXTENSION IF NOT EXISTS vector"]`, `enableSuperuserAccess: false`, storage size and class from values. External mode: `postgres.external.existingSecret` with the same keys as CloudNativePG's app Secret.
- **ensure-secret**: ServiceAccount, Role (`create` on secrets only), RoleBinding, Job named `<fullname>-ensure-secret-<hash of pod spec>`; ordinary resources, not hooks, following llm-serving-pack's `nebari-openmeter/templates/credentials.yaml`. Skipped when `adminKey.existingSecret` is set. The Secret is `adminKey.secretName` (default `mem0-admin-key`), key `api-key`.
- **Caller grant**: Role `<fullname>-admin-key-reader` with `get` on that one Secret (`resourceNames`), RoleBinding to every ServiceAccount listed in `callers[].serviceAccount` in `callers[].namespace`.
- **NetworkPolicies** (when `networkPolicy.enabled`, default true):
  - server ingress: port 8000 from pods matching `callers[].podSelector` in namespace `callers[].namespace`, nothing else (kubelet probes are not subject to NetworkPolicy).
  - token-proxy health port 8082: no ingress rule needed beyond kubelet.
  - database ingress (bundled only): 5432 from the server pods; 5432 and 8000 from the CloudNativePG operator namespace (`postgres.bundled.operatorNamespace`, default `cnpg-system`); 5432 and 8000 between the cluster's own instances.
  - server egress (when `networkPolicy.egress.enabled`, default false): DNS to kube-system, 5432 to the database, plus `networkPolicy.egress.extra` rules for Keycloak and the model endpoint, which depend on the cluster.
- **values.schema.json** so a missing required value fails at render: `models.llm`, `models.embedding.model`, `models.embedding.dimensions`, `models.baseURL`, `models.auth.tokenURL`, `models.auth.existingSecret`, and at least one entry in `callers`.

Values sketch:

```yaml
image: {repository: quay.io/nebari/mem0-pack-server, tag: "", digest: ""}
tools: {image: {repository: quay.io/nebari/mem0-pack-tools, tag: "", digest: ""}}
adminKey: {secretName: mem0-admin-key, existingSecret: ""}
callers:
  - namespace: javo
    serviceAccount: javo-gateway
    podSelector: {app.kubernetes.io/name: javo, app.kubernetes.io/component: gateway}
models:
  baseURL: https://llm-internal.example.com/v1    # llm-serving-pack internal endpoint
  llm: Qwen/Qwen3.5-35B-A3B-GPTQ-Int4
  embedding: {model: "", dimensions: null}
  auth: {tokenURL: "", scope: "", existingSecret: ""}  # Secret keys client-id, client-secret
postgres:
  bundled: {enabled: true, instances: 1, imageName: "...", storage: {size: 10Gi, storageClass: ""}, operatorNamespace: cnpg-system}
  external: {existingSecret: ""}
history: {sizeLimit: 1Gi}
networkPolicy: {enabled: true, egress: {enabled: false, extra: []}}
resources: {...}
```

### Template cleanup (issue #4)

Remove `examples/` (template), `docs/site/`, `.github/workflows/docs.yml`, `docs-preview-cleanup.yml` and `test-integration.yaml`. They target the template's `my-pack` examples, and `docs.yml` deploys to the template's own Cloudflare Pages project. Keep `docs/*.md` (template reference specs). Replace `dev/Makefile` with targets for this pack. `release.yaml` stays disabled (`if: false`) but is repointed at `chart/` with `chart-name: nebari-mem0` and both image tag paths, ready for #12. `build-images.yaml` builds `server` and `tools`.

### CI (issue #11)

- `lint.yaml`: `helm lint`, kubeconform on renders, deterministic render check (two renders diffed), `check-pack-metadata.sh` (langfuse-pack's, tolerant of the private schema), `ruff`, `go vet`.
- `test.yaml`: `helm unittest chart/`, `pytest image/tests`, `go test ./...` in `tools/`.
- `e2e.yaml`: `tests/e2e/kind-e2e.sh`: kind cluster, CloudNativePG chart 0.29.0, build and load both images and the fake model, install the chart, run `tests/e2e/run.sh` (the journeys). Runs on PRs touching `chart/`, `image/`, `tools/`, `tests/e2e/` or the workflow.
- `build-images.yaml`: builds both images on PRs (no push) and pushes on `main`.

### Fake model (`tests/e2e/fake-model/`)

A Python stdlib HTTP server, test-only:

- `POST /token`: client credentials. Checks the client id and secret, returns `{"access_token", "expires_in": 20, "token_type": "Bearer"}`. Short life so the e2e proves refresh.
- `POST /v1/chat/completions` and `/v1/embeddings`: require a bearer token it issued and that hasn't expired, else 401. Chat returns `{"memory":[{"text": ...}]}` built from the user lines in mem0's extraction prompt. Embeddings are deterministic hashed bag-of-words vectors of `DIMS` length, normalized, so a query sharing words with a memory ranks it first.
- `GET /calls`: a log of every call with the token it carried and whether it was valid, for assertions.

### Docs and contract (issue #13)

- `README.md`: what it deploys, prerequisites (NIC with CloudNativePG, llm-serving-pack with a chat and an embedding model on the internal endpoint with an audience, a Keycloak service-account client), install, values reference, the Tapas contract, known limitations (no per-person access control in mem0; history and raw messages kept on scratch disk until the pod restarts; one replica; embedding dimension fixed at first install; no spaCy, so no entity extraction and semantic-only search; the Keycloak client is a prerequisite the pack doesn't create).
- `examples/nebari-values.yaml` and `examples/argocd-app.yaml`: deploy with only hostnames, model names and Secret names changed.
- `docs/architecture/contracts.md` with the contracts below, and the `AGENTS.md` pointer.

## Program Design
**Scale:** full

### Existing patterns
| Pattern | Tag | Source | Promote? |
|---|---|---|---|
| Releases and image builds go through `nebari-dev/.github` reusable workflows; a `Chart.yaml` bump on main triggers both, and the release pins image tags to `sha-<commit>` | [documented: AGENTS.md "Release and images"] | .github/workflows/release.yaml, build-images.yaml | n/a |
| `pack-metadata.yaml` validates against the dashboard schema and declares level, owner, `nebariapp_integration` | [documented: docs/release-readiness-checklist.md "Pack Metadata File"] | pack-metadata.yaml | n/a |
| Images pinned by tag or digest; non-root, read-only root fs, no privilege escalation; NetworkPolicy | [documented: docs/release-readiness-checklist.md lines 190-192] | docs/release-readiness-checklist.md | n/a |
| mem0 has no user-facing route, so no NebariApp | [documented: pack-metadata.yaml, AGENTS.md "Pack design constraints"] | pack-metadata.yaml | n/a |
| Deterministic renders with in-cluster secrets: no `lookup` or random functions in templates | [inferred] | llm-serving-pack `charts/nebari-llm-serving/charts/nebari-openmeter/templates/credentials.yaml` header; langfuse-pack `examples/argocd-app.yaml` header | yes |
| Ensure-secret Job as ordinary resources: create-only Role and a pod-spec hash in the Job name | [inferred] | llm-serving-pack `nebari-openmeter/templates/credentials.yaml` | yes |
| Pack layout as langfuse-pack: `chart/` with helm-unittest suites and wiring scripts, `examples/` with deploy values and an ArgoCD Application | [inferred] | langfuse-pack `chart/`, `examples/`, `.github/workflows/test.yaml` | yes |
| Bundled Postgres as a CloudNativePG Cluster read through its generated app Secret | [inferred] | nexus.internal.openteams.app `manifests/billing/usage-control-db.yaml`, llm-serving-pack `database.yaml` (referenced in credentials.yaml) | yes |
| Go tools log with `log/slog` and wrap errors `fmt.Errorf("verb noun: %w", err)` | [inferred] | tapas `internal/telemetry/telemetry.go`, `internal/gateway/authn/authn.go:291` (via the Tapas spec's patterns table) | no |

Promote? values were set by the builder under the owner's delegation (2026-11-25); `yes` rows are written to `docs/architecture/patterns.md` by the first slice.

### Component contracts
| Contract | Status | Owner | Consumers | Verified by | Promote? | Notes |
|---|---|---|---|---|---|---|
| mem0 REST subset behind the allowlist | new | mem0-pack | tapas memory client (to be written, Lane B) | n/a (new) | n/a | Paths and rules in the allowlist table |
| Admin key Secret readable by named callers | new | mem0-pack | tapas gateway (nebari-dev/tapas#15) | n/a (new) | n/a | |
| Chart values callers list | new | mem0-pack | GitOps values overlays | n/a (new) | n/a | One list drives both the RBAC grant and the NetworkPolicy |
| mem0 Service on port 8000 | new | mem0-pack | tapas | n/a (new) | n/a | |
| mem0 `settings` row `config_overrides` (JSON merged over `DEFAULT_CONFIG` at import) | unchanged (implicit) | mem0 v2.2.1 | mem0 `server_state._load_overrides`; our `migrate.py` writes it | `grep -rn "config_overrides" <mem0>/server/*.py` | no | We write it; mem0 reads it. Pinned by the v2.2.1 commit |
| mem0 server env (`POSTGRES_*`, `APP_DB_NAME`, `ADMIN_API_KEY`, `JWT_SECRET`, `OPENAI_API_KEY`, `HISTORY_DB_PATH`, `MEM0_TELEMETRY`, `MEM0_DIR`, `MEM0_TELEMETRY_STATE_PATH`) | unchanged (implicit) | mem0 v2.2.1 | our chart and image | `grep -rhoE 'os\.environ\.get\("[A-Z0-9_]+' <mem0>/server/*.py <mem0>/mem0/memory/*.py \| sort -u` | no | |
| CloudNativePG app Secret keys (`username`, `password`, `host`, `port`, `dbname`) | unchanged | CloudNativePG 1.30 | our chart | n/a (external) | no | |
| Keycloak client-credentials token endpoint | unchanged | Keycloak | token-proxy | n/a (external) | no | |
| llm-serving-pack internal endpoint (`/v1/chat/completions`, `/v1/embeddings`, JWT with groups and audience) | unchanged | llm-serving-pack | token-proxy (on mem0's behalf) | n/a (external) | no | Embeddings route unverified until #3's live run |
| Image names and values tag paths | new | mem0-pack | release.yaml `tag-paths` | n/a (new) | n/a | `quay.io/nebari/mem0-pack-server` and `-tools`; values `image.tag`, `tools.image.tag` |

**mem0 REST subset.** Promises the allowlisted methods and paths with mem0 v2.2.1's request and response shapes, authenticated by `X-API-Key: <admin key>`. Requires every list and search to be scoped: literal, non-empty `user_id`, `agent_id` or `run_id` values, no wildcard, no logical or operator filters. Errors: 404 for anything not allowlisted, 401 without a valid key, 400 for an unscoped list or search, mem0's own 4xx/5xx otherwise. `GET /memories/{id}` answers `200 null` for an id that doesn't exist (`mem0/memory/main.py:1233-1236`). mem0 does not check ownership on by-id calls; the caller must (Tapas spec, Memory section).

**Admin key Secret.** Promises a Secret named by `adminKey.secretName` (default `mem0-admin-key`) in the release namespace with key `api-key`, 64 hex characters, created once and never changed by the chart. Requires callers to be listed in `callers[]`. Rotation: replace the value, restart mem0; callers re-read per tapas#15.

### Data models and types

```go
// tools/internal/tokenproxy
type Config struct {
	Listen       string        // "127.0.0.1:8081"
	HealthListen string        // ":8082"
	Upstream     *url.URL
	TokenURL     string
	ClientIDFile, ClientSecretFile string
	Scope        string
	MinRemaining time.Duration // refresh when remaining < min(30s, 25% of lifetime)
}
type token struct{ value string; expiry time.Time; lifetime time.Duration }

// tools/internal/ensuresecret
type Config struct{ Namespace, Name, Key string }
```

```python
# image/mem0pack/settings.py
@dataclass(frozen=True)
class PackSettings:
    llm_model: str
    embedding_model: str
    embedding_dims: int
    model_base_url: str
    collection: str = "memories"
    def overrides(self) -> dict: ...  # the config_overrides JSON

# image/mem0pack/allowlist.py
@dataclass(frozen=True)
class Rule:
    method: str
    pattern: re.Pattern
    needs_scope: Literal["none", "query", "body"]
```

### Method signatures

```go
// tools/internal/tokenproxy
type TokenSource interface{ Token(ctx context.Context) (string, error) }
type Doer interface{ Do(*http.Request) (*http.Response, error) }
func NewClientCredentials(cfg Config, hc Doer, now func() time.Time) *ClientCredentials // implements TokenSource
func NewHandler(upstream *url.URL, ts TokenSource, log *slog.Logger) *Handler        // http.Handler
func NewHealth(ts *ClientCredentials) http.Handler

// tools/internal/ensuresecret
type SecretCreator interface{ Create(ctx context.Context, ns, name, key string, value []byte) error } // nil on AlreadyExists
func NewAPICreator(apiURL string, hc Doer, tokenFile string) *APICreator
func Ensure(ctx context.Context, c SecretCreator, cfg Config, random io.Reader) error

// tools/cmd/mem0-pack-tools
func main() // subcommands: token-proxy, ensure-secret
```

```python
# image/mem0pack
def load_settings(env: Mapping[str, str]) -> PackSettings
def write_overrides(session_factory, overrides: dict) -> None
def run_migrations(server_dir: Path) -> None
class Allowlist:            # ASGI middleware
    def __init__(self, app, rules: Sequence[Rule]): ...
    async def __call__(self, scope, receive, send): ...
class Health:               # ASGI middleware answering /healthz, /readyz
    def __init__(self, app, ready: Callable[[], bool]): ...
def build_app(server_main) -> ASGIApp
def main() -> None          # serve entrypoint
```

### Program layout

```text
mem0-pack/
+ image/Dockerfile                       server image (follows llm-serving-pack per-image dirs)
+ image/requirements.in, requirements.txt   hash-locked deps
+ image/mem0pack/{__init__,settings,migrate,allowlist,health,serve}.py
+ image/tests/test_{settings,allowlist,health,migrate}.py
+ image/pyproject.toml                   ruff + pytest config only
+ tools/go.mod
+ tools/cmd/mem0-pack-tools/main.go
+ tools/internal/tokenproxy/{config,source,proxy,health}.go + _test.go
+ tools/internal/ensuresecret/{ensure,api}.go + _test.go
+ tools/Dockerfile                       distroless static nonroot
+ chart/Chart.yaml, values.yaml, values.schema.json   (follows langfuse-pack chart/)
+ chart/templates/{_helpers.tpl,deployment.yaml,service.yaml,database.yaml,ensure-secret.yaml,caller-grant.yaml,networkpolicy.yaml,NOTES.txt}
+ chart/tests/{deployment,database,ensure-secret,caller-grant,networkpolicy}_test.yaml
+ chart/tests/{check-pack-metadata.sh,deterministic.sh}
+ tests/e2e/{kind-e2e.sh,run.sh,ci-values.yaml,egress-values.yaml,external-values.yaml}
+ tests/e2e/fake-model/{server.py,Dockerfile}
+ examples/{nebari-values.yaml,argocd-app.yaml}
+ docs/architecture/{contracts.md,patterns.md}
~ AGENTS.md, README.md, dev/Makefile
~ .github/workflows/{lint,test,build-images,release}.yaml
+ .github/workflows/e2e.yaml
- examples/{vanilla-yaml,kustomize-nginx,basic-nginx,auth-fastapi,wrap-existing-chart}
- docs/site/, .github/workflows/{docs.yml,docs-preview-cleanup.yml,test-integration.yaml}
```

### Call stacks

```text
Production:
  Tapas gateway start
    → GET /api/v1/namespaces/<mem0 ns>/secrets/mem0-admin-key   (its own SA; caller-grant Role)

Production:
  POST /memories  (Tapas, X-API-Key)
    → NetworkPolicy admits caller pod
    → Health (pass-through) → Allowlist (rule match) → mem0 main.app add_memory
      → verify_auth (ADMIN_API_KEY)
      → Memory.add → SQLite get_last_messages
        → OpenAI embedder → http://127.0.0.1:8081/v1/embeddings
          → token-proxy Handler → ClientCredentials.Token (cache or POST TOKEN_URL)
          → llm-serving-pack internal /v1/embeddings (Bearer service token)
        → pgvector search (Postgres, CloudNativePG)
        → OpenAI LLM → 127.0.0.1:8081/v1/chat/completions → token-proxy → llm-serving-pack
        → pgvector insert, SQLite history + messages

Production:
  POST /search, GET /memories, GET|DELETE /memories/{id}
    → same path to mem0; search embeds the query through token-proxy

Production:
  Pod start
    → init migrate: mem0pack.migrate → alembic upgrade head → write_overrides(settings.config_overrides)
    → init token-proxy (native sidecar): first token fetch, /healthz 200
    → server: mem0pack.serve → import main (initialize_state reads overrides) → uvicorn

Production:
  Install / sync
    → Job ensure-secret → mem0-pack-tools ensure-secret → POST /api/v1/namespaces/<ns>/secrets (409 = done)
    → CloudNativePG Cluster → initdb → postInitApplicationSQL CREATE EXTENSION vector → <name>-db-app Secret

Test:
  e2e: same as production, with models.baseURL and models.auth.tokenURL pointing at fake-model in-cluster
```

### Component trees and DI

```text
mem0-pack-tools main
  token-proxy:  cfg := tokenproxy.LoadConfig(flags, env)
                cc  := tokenproxy.NewClientCredentials(cfg, &http.Client{Timeout: 10s}, time.Now)
                srv := tokenproxy.NewHandler(cfg.Upstream, cc, slog)   // takes TokenSource
                health := tokenproxy.NewHealth(cc)
  ensure-secret: c := ensuresecret.NewAPICreator(inClusterURL, &http.Client{TLS: in-cluster CA}, saTokenFile)
                 ensuresecret.Ensure(ctx, c, cfg, crypto/rand.Reader)   // takes SecretCreator, io.Reader

mem0pack.serve
  settings := load_settings(os.environ)            (used by migrate only)
  main     := import server main (mem0's composition root)
  app      := Health(Allowlist(main.app, RULES), ready=db_ping(main.SessionLocal))
```

### Test approach and seams
| Boundary | Seam | Real/fake | Level | Proves |
|---|---|---|---|---|
| Token fetch and refresh | `TokenSource`, `Doer`, `now` | fake HTTP doer, fake clock | unit (Go, table-driven) | caching, refresh margin, no forward without token, 502 on failure |
| Proxy forwarding | `TokenSource` | fake source, `httptest` upstream | unit | Authorization replaced, path and body forwarded, caller's header dropped |
| Ensure secret | `SecretCreator`, `io.Reader` | fake creator; `httptest` API for `APICreator` | unit | create once, 409 is success, value never read |
| Allowlist | ASGI app | dummy ASGI app | unit (pytest) | every allowed rule passes, everything else 404, scope rules 400 |
| Health | `ready` callable | stub | unit | /healthz, /readyz behaviour |
| Settings and overrides | env mapping, session factory | dict env, SQLite session | unit | override JSON shape; upsert idempotent |
| Chart templates | helm-unittest | rendered manifests | unit | wiring, schema failures, RBAC resourceNames, NetworkPolicy selectors, no hooks, deterministic |
| Whole pack | kind + CloudNativePG + fake model | real mem0, real Postgres, fake model and token endpoint | e2e | the journeys |

## Journeys

Defined by the builder under the owner's delegation (2026-11-25). Environment: kind with kindnet NetworkPolicy enforcement (kind v0.24+, kube-network-policies), CloudNativePG 1.30, fake model. "Caller" means a pod running as the ServiceAccount and labels listed in `callers[]`, in another namespace, standing in for the Tapas gateway.

| # | Item | Proof | Check method | Evidence |
|---|------|-------|--------------|----------|
| 1 | An operator installs the pack without making the admin key or database credentials by hand | In a fresh namespace labelled `pod-security.kubernetes.io/enforce=restricted`, `helm install` with `tests/e2e/ci-values.yaml`, where the only pre-created Secret is the model client's credentials (a documented prerequisite), gives: CloudNativePG cluster `Ready`, mem0 Deployment available within 5 minutes, Secret `mem0-admin-key` with a 64-hex-character `api-key` | automated: `tests/e2e/run.sh` step `install` | |
| 2 | The caller gets the key through the grant, and nobody else does | The caller pod reads `mem0-admin-key` via the Kubernetes API with its own token (200); a pod with a different ServiceAccount in the same namespace gets 403; `kubectl auth can-i get secret/mem0-admin-key -n <ns> --as system:serviceaccount:<caller-ns>:<other-sa>` says no | automated: `run.sh` step `grant` | |
| 3 | The caller adds, finds, reads and deletes one person's memory, and other people's stay separate | Caller POSTs a two-message conversation for `user_id=alice, agent_id=helper`; `POST /search` for alice returns a memory containing the fact; search for `user_id=bob` returns none; `GET /memories/{id}` returns it with `user_id: alice`; `DELETE /memories/{id}` succeeds and a repeat search no longer returns it | automated: `run.sh` step `memories` | |
| 4 | mem0's model calls carry a fresh service token, and mem0 never holds the client secret | The fake model's `/calls` shows every chat and embedding call carried a valid unexpired token; after waiting past the 20 s token life, another add succeeds and `/calls` shows a second token; the `server` container's spec and env mention neither the client secret value nor its Secret | automated: `run.sh` step `token` | |
| 5 | Memories survive a restart of mem0 | Delete the mem0 pod; once the new one is Ready, alice's earlier search result comes back | automated: `run.sh` step `restart` | |
| 6 | Dangerous endpoints are closed even with the key | With a valid key: `POST /auth/register`, `POST /configure`, `POST /reset`, `GET /api-keys`, `GET /entities`, `GET /requests`, `GET /docs`, `GET /openapi.json`, `POST /generate-instructions`, `GET /memories/{id}/history`, `PUT /memories/{id}`, `DELETE /memories?user_id=alice` all return 404; `GET /memories` and `POST /search` with no scope, with `user_id=*`, with an empty `user_id`, or with an `OR` filter return 400; allowed paths without a key return 401 | automated: `run.sh` step `allowlist` and `pytest image/tests/test_allowlist.py` | |
| 7 | Only named callers reach mem0, and only mem0 reaches its database | From a pod in a namespace not in `callers[]`, a TCP connect to mem0:8000 times out (5 s); the caller connects; from the caller pod, a TCP connect to `<fullname>-db-rw:5432` times out | automated: `run.sh` step `netpol` | |
| 8 | mem0 works with every outbound path closed except DNS, its database, and the model and token endpoints | A second install with `tests/e2e/egress-values.yaml` (`networkPolicy.egress.enabled: true`, extra rule to the fake model only) passes steps `memories` and `token`; the image's `MEM0_TELEMETRY` is `false` | automated: `run.sh` step `egress` | |
| 9 | Renders never drift, and upgrades keep the key | Two `helm template` runs with the same values are byte-identical; after `helm upgrade` with the same values, `mem0-admin-key` has the same `api-key` and `resourceVersion` | automated: `chart/tests/deterministic.sh` in CI and `run.sh` step `upgrade` | |
| 10 | An operator can use their own Postgres instead of the bundled one | With `postgres.bundled.enabled: false` and `postgres.external.existingSecret` naming a separately created CloudNativePG cluster's app Secret (`tests/e2e/external-values.yaml`), install succeeds, no `Cluster` is rendered by the chart, and step `memories` passes | automated: `run.sh` step `external` | |
| 11 | The running image is mem0 v2.2.1, hardened | Inside the server container, `pip show mem0ai` reports 2.2.1 and `/app/server/.mem0-commit` is `94c3fe9f238f3dbf29c9ce98643bd71eb13077cd`; the process runs as UID 10001; writing to `/app` fails (read-only); the rendered chart has no `:latest` image and pods were admitted under `restricted` (journey 1) | automated: `run.sh` step `image` | |
| 12 | Every PR proves the pack before review | On this work's PR, the lint, test, build-images and e2e checks all run and pass | narrated: `gh pr checks <n>` transcript | |
| 13 | An operator can install from the docs alone | README has prerequisites, install, values reference, the Tapas contract and known limitations; `examples/nebari-values.yaml` renders with `helm template` and passes kubeconform with only hostnames, model names and Secret names to change; `docs/architecture/contracts.md` lists the mem0 contracts | automated: `chart/tests/values-wiring.sh` renders the example; narrated: README section headings transcript | |
| 14 | Nothing in the repo can publish the template's leftovers | No workflow references `examples/auth-fastapi`, `my-pack`, `nebari-software-pack-template` or `building-a-software-pack`; `release.yaml` targets `chart/` and is disabled (`if: false`) until #12 | automated: `grep` check in `lint.yaml` | |

## Left to the builder

- Exact pinned digests (Python base, distroless, CloudNativePG image, kind node image) and exact dependency versions, resolved at build time and recorded in the files.
- Default resource requests and limits.
- How the fake model reads mem0's extraction prompt (from `generate_additive_extraction_prompt` at v2.2.1).
- Whether mem0's embedder needs `embedding_dims` set as well as the vector store's `embedding_model_dims`; the e2e will show it with a non-1536 dimension (384 in CI).

## Open questions (owner)

None blocking. Recorded for later: a Postgres-backed history store with deletion rules (scale and durability, wanted later); whether #9 should block #12 now that the allowlist ships before the first release (it does in this plan).
