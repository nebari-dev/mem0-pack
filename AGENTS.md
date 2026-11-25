# AGENTS.md

This file provides guidance to coding agents working with code in this repository.

## What this repo is right now

This is the mem0 Nebari software pack, at the very start of its life. It was created from
`nebari-dev/nebari-software-pack-template`, and only `README.md`, `pack-metadata.yaml` and
`CODEOWNERS` have been made mem0-specific so far. Everything else is still template material:

- `examples/` (vanilla-yaml, kustomize-nginx, basic-nginx, auth-fastapi, wrap-existing-chart) are
  the template's reference packs, all named `my-pack`. None of them is mem0.
- `.github/workflows/`, `dev/Makefile` and `docs/site/` all target those examples and still carry
  template identifiers (`CF_PROJECT: nebari-software-pack-template`, `PACK_SLUG:
  building-a-software-pack`, release `chart-name: my-pack`, the auth-fastapi image).
- `docs/*.md` (auth flow, NebariApp CRD reference, release-readiness checklist, docs-site
  requirements) are template reference docs. They are useful as specs, not as pack docs.

The mem0 chart does not exist yet. When it lands, the workflows, Makefile targets and docs site
must be repointed at it; check each one rather than assuming it already covers the pack.

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

## Commands

CI (`.github/workflows/lint.yaml`) validates charts and manifests like this; mirror it for any
new chart:

```sh
helm dependency build <chart>/
helm lint <chart>/
helm template test <chart>/ [--set ...]          # render each meaningful values combination
kubectl kustomize <overlay>/ | kubeconform -strict -ignore-missing-schemas
```

`test.yaml` installs charts into a plain kind cluster and curls them via port-forward.
`test-integration.yaml` builds a kind cluster with MetalLB, Envoy Gateway, cert-manager, Keycloak
and nebari-operator (pinned `OPERATOR_REF: v0.1.0-alpha.19`, also pinned in `dev/Makefile`; keep
them in sync) and waits for NebariApps to go Ready.

Local cluster (from `dev/`; needs docker, kind, helm, kubectl, git):

```sh
make cluster        # kind cluster "my-pack-dev" with the full Nebari stack (clones nebari-operator into dev/.cache)
make up-basic       # or up-vanilla / up-kustomize / up-fastapi / up-podinfo (template examples)
make down
```

Docs site (`docs/site/`, Astro + Starlight + `@nebari/starlight`, uses bun):

```sh
cd docs/site
bun install --frozen-lockfile
bun run dev
bun run build                         # BASE_PATH defaults to /building-a-software-pack/
bun test test                         # all tests (build.test.ts builds the site first)
bun test test/rehype-base-links.test.ts   # a single test file
```

`test/build.test.ts` hardcodes the base path and every page title, so renaming the slug or a page
means updating those tests deliberately. `docs/pack-docs-requirements.md` requires the Astro
`base` to be `/<repo-name>/` (here `/mem0-pack/`) and a specific sidebar layout. The docs site is
not required until GA; `pack-metadata.yaml` currently has `docs_site: false`.

## Release and images

Releases go through reusable workflows in `nebari-dev/.github` (`pack-release.yaml@v1`,
`pack-build-image.yaml@v1`). A `Chart.yaml` version bump on `main` triggers both: the release
pins image tags to `sha-<release-commit>` and opens a PR in `nebari-dev/helm-repository`, so the
image build must also trigger on that `Chart.yaml` path or the pinned tag will not exist.
