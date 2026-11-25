# Architecture patterns

Patterns this pack follows, confirmed by the owner's delegate. Each entry names its source so it can be re-checked.

### Deterministic renders with in-cluster secrets: no `lookup` or random functions in templates

Chart templates must render identically on every run, so they never call `lookup`, `randAlphaNum` or similar. Secrets are created in-cluster by a Job instead, which keeps `helm template` and ArgoCD diffs stable.

**Source:** llm-serving-pack `charts/nebari-llm-serving/charts/nebari-openmeter/templates/credentials.yaml` (header); langfuse-pack `examples/argocd-app.yaml` (header)

**Confirmed:** 2026-11-25 (spec: docs/superpowers/specs/2026-11-25-mem0-pack-design.md)

### Ensure-secret Job as ordinary resources: create-only Role and a pod-spec hash in the Job name

The ensure-secret Job is a normal chart resource (not a hook) with a Role that can only create Secrets, so it never overwrites an existing one. The Job name carries a hash of its pod spec, so a changed spec produces a new Job instead of an immutable-field error.

**Source:** llm-serving-pack `nebari-openmeter/templates/credentials.yaml`

**Confirmed:** 2026-11-25 (spec: docs/superpowers/specs/2026-11-25-mem0-pack-design.md)

### Pack layout as langfuse-pack: `chart/` with helm-unittest suites and wiring scripts, `examples/` with deploy values and an ArgoCD Application

The chart lives in `chart/` with helm-unittest suites and wiring scripts beside it. `examples/` holds deploy values and an ArgoCD Application so the pack can be installed the way operators will install it.

**Source:** langfuse-pack `chart/`, `examples/`, `.github/workflows/test.yaml`

**Confirmed:** 2026-11-25 (spec: docs/superpowers/specs/2026-11-25-mem0-pack-design.md)

### Bundled Postgres as a CloudNativePG Cluster read through its generated app Secret

The bundled database is a CloudNativePG `Cluster`, not a hand-rolled StatefulSet. Workloads read credentials and connection details from the app Secret that CloudNativePG generates (`username`, `password`, `host`, `port`, `dbname`).

**Source:** llm-serving-pack `charts/nebari-llm-serving/charts/nebari-openmeter/templates/database.yaml`

**Confirmed:** 2026-11-25 (spec: docs/superpowers/specs/2026-11-25-mem0-pack-design.md)
