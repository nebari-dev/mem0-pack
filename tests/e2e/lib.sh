#!/usr/bin/env bash
# Shared helpers for tests/e2e/run.sh. Sourced, not executed.

# Hard-coded: never take the kube context from an inherited environment variable, and only ever
# touch the e2e cluster.
CONTEXT=kind-mem0-e2e
NS=mem0
RELEASE=mem0
FULL=mem0-nebari-mem0
KEY_SECRET=mem0-admin-key
CALLER_NS=tapas-sim
DB_CLUSTER="${FULL}-db"
MEM0_URL="http://${FULL}.${NS}.svc:8000"
FAKE_MODEL_URL="http://fake-model.fake-model.svc:8080"
E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "$E2E_DIR/../../chart" && pwd)"

k() { kubectl --context "$CONTEXT" "$@"; }

# use_release <namespace> <release> [database cluster]: point every helper at another release of
# the chart. The defaults above describe release mem0 in namespace mem0.
use_release() {
  NS="$1"
  RELEASE="$2"
  FULL="${2}-nebari-mem0"
  DB_CLUSTER="${3:-${FULL}-db}"
  MEM0_URL="http://${FULL}.${NS}.svc:8000"
}

# step <journey> <description>: announce a step; fail <journey> <message>: abort naming the journey.
step() { echo "--- journey $1: $2"; }
fail() { echo "FAIL journey $1: $2" >&2; exit 1; }

# assert_eq <journey> <what> <want> <got>
assert_eq() {
  [ "$3" = "$4" ] || fail "$1" "$2: want '$3', got '$4'"
}

# kexec_caller <pod> <python args...>: run the python program on stdin inside a tapas-sim pod
# (the pods run the fake-model image, which has python and no curl).
kexec_caller() {
  local pod="$1"; shift
  k -n "$CALLER_NS" exec -i "$pod" -- python - "$@"
}

# k8s_get <pod> <path>: GET the Kubernetes API with the pod's own projected token.
# Prints the HTTP status on the first line and the body after it.
k8s_get() {
  local pod="$1" path="$2"
  kexec_caller "$pod" "$path" <<'PY'
import ssl, sys, urllib.error, urllib.request
sa = "/var/run/secrets/kubernetes.io/serviceaccount/"
token = open(sa + "token").read().strip()
ctx = ssl.create_default_context(cafile=sa + "ca.crt")
req = urllib.request.Request("https://kubernetes.default.svc" + sys.argv[1],
                             headers={"Authorization": "Bearer " + token})
try:
    r = urllib.request.urlopen(req, context=ctx, timeout=15)
    code, body = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    code, body = e.code, e.read().decode()
print(code)
print(body)
PY
}

# admin_key: read the admin key the way the caller does, via the Kubernetes API with its own token.
admin_key() {
  local out
  out="$(k8s_get caller "/api/v1/namespaces/${NS}/secrets/${KEY_SECRET}")" || return 1
  [ "$(echo "$out" | head -1)" = 200 ] || return 1
  echo "$out" | tail -n +2 | jq -r '.data["api-key"]' | base64 -d
}

# api <method> <path> [json-body] [key]: call mem0 from the caller pod. The key defaults to the
# admin key; pass the literal word NOKEY to send none. Prints the status then the body.
api() {
  local method="$1" path="$2" body="${3:-}" key="${4:-}"
  if [ -z "$key" ]; then key="$(admin_key)" || fail "?" "caller could not read the admin key"; fi
  kexec_caller caller "$method" "${MEM0_URL}${path}" "$body" "$key" <<'PY'
import sys, urllib.error, urllib.request
method, url, body, key = sys.argv[1:5]
headers = {}
if key != "NOKEY":
    headers["X-API-Key"] = key
data = None
if body:
    data = body.encode()
    headers["Content-Type"] = "application/json"
req = urllib.request.Request(url, data=data, method=method, headers=headers)
try:
    r = urllib.request.urlopen(req, timeout=120)
    code, text = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    code, text = e.code, e.read().decode()
print(code)
print(text)
PY
}

# root_mount_is_ro: read /proc/self/mounts on stdin; succeed when the "/" mount has the ro option.
root_mount_is_ro() {
  awk '$2 == "/" { n = split($4, o, ","); for (i = 1; i <= n; i++) if (o[i] == "ro") found = 1 } END { exit !found }'
}

# status_of / body_of split an api result.
status_of() { head -1 <<<"$1"; }
body_of() { tail -n +2 <<<"$1"; }

# wait_ready: the database (bundled, or the external one named by use_release) and the mem0 Deployment are up (journey 1 timing: 5 minutes).
wait_ready() {
  k -n "$NS" wait --for=condition=Ready "cluster.postgresql.cnpg.io/${DB_CLUSTER}" --timeout=300s
  k -n "$NS" rollout status "deploy/${FULL}" --timeout=300s
}

# server_pod: name of the live mem0 server pod (owned by a ReplicaSet, not the ensure-secret Job
# pod, which carries the same instance label and stays Completed).
server_pod() {
  k -n "$NS" get pod -l "app.kubernetes.io/instance=$RELEASE" -o json \
    | jq -r '[.items[] | select(.metadata.deletionTimestamp == null)
              | select(any(.metadata.ownerReferences[]?; .kind == "ReplicaSet"))][0].metadata.name'
}

# fake_calls: the fake model's call log, fetched from the caller pod.
fake_calls() {
  kexec_caller caller "${FAKE_MODEL_URL}/calls" <<'PY'
import sys, urllib.request
print(urllib.request.urlopen(sys.argv[1], timeout=15).read().decode())
PY
}

# tcp_probe <namespace> <pod> <host> <port> [container]: TCP connect from inside a pod (optionally a
# named container) with a 5 s timeout.
# Prints exactly one word: connected, timeout (dropped), or refused:<error> for anything else, so a
# dropped path is never confused with a reachable one or with a broken probe.
tcp_probe() {
  local ns="$1" pod="$2" cargs=()
  [ -z "${5:-}" ] || cargs=(-c "$5")
  k -n "$ns" exec -i "${cargs[@]}" "$pod" -- python - "$3" "$4" <<'PY'
import socket, sys
try:
    socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=5).close()
    print("connected")
except socket.timeout:
    print("timeout")
except OSError as e:
    print("refused:" + repr(e))
PY
}

# restart_mem0: roll the mem0 Deployment and wait until the new pod is ready and the old one gone.
restart_mem0() {
  k -n "$NS" rollout restart "deploy/${FULL}" >/dev/null || return 1
  k -n "$NS" rollout status "deploy/${FULL}" --timeout=300s || return 1
  k -n "$NS" wait --for=condition=Ready "pod/$(server_pod)" --timeout=300s
}
