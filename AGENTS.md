# AGENTS.md

This file provides guidance to coding agents working with code in this repository.

## What this repo is right now

This is the mem0 Nebari software pack. It was created from `nebari-dev/nebari-software-pack-template`;
the template's examples, docs site and their workflows have been removed. What is here:

- `image/`: the mem0 server image (Python, with its tests, `requirements*.txt` and `smoke.sh`).
- `tools/`: a Go module for the tools image (token proxy sidecar and the create-once secret
  generator).
- `tests/e2e/fake-model/`: a stdlib Python fake model server for end-to-end tests.
- `.github/`: workflows for lint, unit tests, image builds and release, plus
  `.github/scripts/check-template-leftovers.sh`, which fails if template identifiers return to
  `.github/` or `dev/` or if `release.yaml`'s `release` job is not disabled (`if: false`).
- `docs/architecture/` holds this pack's contracts. `docs/*.md` (auth flow, NebariApp CRD
  reference, release-readiness checklist, docs-site requirements) are template reference docs:
  useful as specs, not as pack docs. `docs/superpowers/` holds design specs and plans.

The Helm chart does not exist yet; it arrives in the next slice at `chart/` (only
`chart/tests/check-pack-metadata.sh` is there now). `release.yaml` already points at `chart/` with
chart name `nebari-mem0` but stays disabled until the first release is cut. `dev/Makefile` targets
(`kind-up`, `e2e`, `down`) call `tests/e2e/kind-e2e.sh`, which does not exist yet.


## Pack design constraints (from README.md and pack-metadata.yaml)

- Deploys the mem0 server, Postgres with pgvector, an admin API key in a Kubernetes Secret, and a
  NetworkPolicy that admits only named callers (for Tapas, its gateway pods).
- mem0 has no per-user access control: any holder of the API key can read every person's
  memories. Protection rests on keeping the key secret and the network path closed. The calling
  service (Tapas first) scopes each request to a person. Do not add anything that widens either.
- mem0 is an internal API with no user-facing route, so `nebariapp_integration: none` and there
  is no NebariApp. The template's NebariApp/Keycloak machinery does not apply to the mem0 chart.
- Maturity is `level: experimental` (see `docs/release-readiness-checklist.md` for what each
  promotion requires, including `last_promoted_at` / `last_promoted_pr` updates).
  `pack-metadata.yaml` must validate against the dashboard schema:

```sh
check-jsonschema \
  --schemafile https://raw.githubusercontent.com/nebari-dev/software-pack-dashboard/main/schema/pack-metadata.schema.json \
  pack-metadata.yaml
```

Contracts are documented in `docs/architecture/contracts.md`. Changing a contract updates that file in the same PR.

## Commands

CI mirrors these; run them from the repo root. `.github/workflows/lint.yaml`:

```sh
bash .github/scripts/check-template-leftovers.sh
bash chart/tests/check-pack-metadata.sh          # needs pyyaml and check-jsonschema
uvx --from check-jsonschema check-jsonschema --builtin-schema vendor.github-workflows .github/workflows/*.y*ml
(cd image && uvx ruff@0.17.6 check . && uvx ruff@0.17.6 format --check .)
uvx ruff@0.17.6 check tests/e2e/fake-model && uvx ruff@0.17.6 format --check tests/e2e/fake-model
(cd tools && go vet ./... && gofmt -l .)         # gofmt must print nothing
```

`.github/workflows/test.yaml` (unit tests):

```sh
(cd image && uv run --no-project --with-requirements requirements-dev.txt pytest -q)
uv run --no-project --with pytest pytest -q tests/e2e/fake-model
(cd tools && go test ./... -race -count=1)
```

Ruff is pinned to 0.17.6 (`required-version` in `image/pyproject.toml`); keep CI and local the same.
Third-party actions are pinned to a full commit SHA with a version comment.

Chart checks (`helm lint`, `helm template`, `kubeconform`) and the kind end-to-end run are not
wired up yet; they land with the chart and `tests/e2e/kind-e2e.sh`. When they do, add them to
`lint.yaml` and to this section.


## Release and images

Releases go through reusable workflows in `nebari-dev/.github` (`pack-release.yaml@v1`,
`pack-build-image.yaml@v1`). A `Chart.yaml` version bump on `main` triggers both: the release
pins image tags to `sha-<release-commit>` and opens a PR in `nebari-dev/helm-repository`, so the
image build must also trigger on that `Chart.yaml` path or the pinned tag will not exist.
