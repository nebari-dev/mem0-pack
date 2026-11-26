#!/usr/bin/env bash
# Bootstrap and drive the kind cluster used for the pack's end-to-end tests.
#
#   kind-e2e.sh up     create (or reuse) the cluster, operator, fake model, callers
#   kind-e2e.sh test   run tests/e2e/run.sh (extra args are passed through)
#   kind-e2e.sh down   delete the cluster
#   kind-e2e.sh        up, test, then down (set KEEP_CLUSTER=1 to skip the teardown)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"

CLUSTER=mem0-e2e
CONTEXT="kind-${CLUSTER}"
NODE_IMAGE="kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0"
CNPG_VERSION=0.29.0
CNPG_REPO=https://cloudnative-pg.github.io/charts
# Operator image pinned by digest (ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0).
CNPG_IMAGE_TAG="1.30.0@sha256:a2701eb97cdd2a34b1fdb2cb51987f544b706e40bec72ae7146cd8580efefebb"
DUMP_NAMESPACES=(mem0 mem0-ext fake-model tapas-sim cnpg-system)

k() { kubectl --context "$CONTEXT" "$@"; }

dump() {
  echo "=== failure: dumping cluster state ===" >&2
  kind get clusters 2>/dev/null | grep -qx "$CLUSTER" || return 0
  local ns pod c
  for ns in "${DUMP_NAMESPACES[@]}"; do
    k get ns "$ns" >/dev/null 2>&1 || continue
    echo "--- namespace $ns: pods ---" >&2
    k -n "$ns" get pods -o wide >&2 || true
    echo "--- namespace $ns: events ---" >&2
    k -n "$ns" get events --sort-by=.lastTimestamp >&2 || true
    for pod in $(k -n "$ns" get pods -o name 2>/dev/null); do
      for c in $(k -n "$ns" get "$pod" -o jsonpath='{.spec.initContainers[*].name} {.spec.containers[*].name}' 2>/dev/null); do
        echo "--- logs $ns/$pod/$c ---" >&2
        k -n "$ns" logs "$pod" -c "$c" --tail=200 >&2 || true
      done
    done
  done
}

cluster_exists() { kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; }

build_and_load() {
  docker build -q -t mem0-pack-server:dev "$ROOT/image"
  docker build -q -t mem0-pack-tools:dev "$ROOT/tools"
  docker build -q -t fake-model:dev "$HERE/fake-model"
  kind load docker-image --name "$CLUSTER" mem0-pack-server:dev mem0-pack-tools:dev fake-model:dev
}

up() {
  trap 'rc=$?; [ $rc -ne 0 ] && dump; exit $rc' EXIT
  if cluster_exists; then
    echo "reusing kind cluster $CLUSTER"
  else
    kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --wait 120s
  fi
  k wait --for=condition=Ready node --all --timeout=120s

  build_and_load

  helm --kube-context "$CONTEXT" repo add cnpg "$CNPG_REPO" --force-update >/dev/null
  helm --kube-context "$CONTEXT" repo update cnpg >/dev/null
  helm --kube-context "$CONTEXT" upgrade --install cnpg cnpg/cloudnative-pg \
    --version "$CNPG_VERSION" --set-string image.tag="$CNPG_IMAGE_TAG" --namespace cnpg-system --create-namespace --wait --timeout 300s
  k -n cnpg-system rollout status deploy --timeout=300s
  k wait --for=condition=Established crd/clusters.postgresql.cnpg.io --timeout=120s

  k apply -f "$HERE/fake-model/manifest.yaml"
  k -n fake-model rollout status deploy/fake-model --timeout=180s

  k apply -f "$HERE/caller.yaml"
  k -n tapas-sim wait --for=condition=Ready pod/caller pod/stranger --timeout=180s
  k -n outsider wait --for=condition=Ready pod/outsider --timeout=180s

  k create namespace mem0 --dry-run=client -o yaml | k apply -f -
  k label namespace mem0 pod-security.kubernetes.io/enforce=restricted --overwrite
  k -n mem0 create secret generic model-client \
    --from-literal=client-id=mem0 --from-literal=client-secret=secret \
    --dry-run=client -o yaml | k apply -f -

  trap - EXIT
  echo "cluster $CLUSTER is ready"
}

run_tests() {
  trap 'rc=$?; [ $rc -ne 0 ] && dump; exit $rc' EXIT
  bash "$HERE/run.sh" "$@"
  trap - EXIT
}

down() { kind delete cluster --name "$CLUSTER"; }

case "${1:-all}" in
  up) up ;;
  test) shift; run_tests "$@" ;;
  down) down ;;
  all)
    if [ "${KEEP_CLUSTER:-}" != "1" ]; then trap 'down' EXIT; fi
    (up)
    (run_tests)
    ;;
  *) echo "usage: $0 [up|test|down]" >&2; exit 2 ;;
esac
