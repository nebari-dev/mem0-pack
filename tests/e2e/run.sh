#!/usr/bin/env bash
# Journey steps for the end-to-end run: run.sh [STEP...]
# Steps: install grant memories token restart allowlist upgrade image netpol egress
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"

ALICE_FACT="Alice loves hiking in the mountains every summer"
ALICE_QUERY="hiking mountains"

# search_json <user_id>: run the search and print the body; the step fails unless the call is
# HTTP 200 and the body holds a results list.
search_json() {
  local out body
  out="$(api POST /search "{\"query\":\"$ALICE_QUERY\",\"filters\":{\"user_id\":\"$1\"}}")"
  [ "$(status_of "$out")" = 200 ] || fail "${JOURNEY:-?}" "search for $1 returned $(status_of "$out"): $(body_of "$out")"
  body="$(body_of "$out")"
  jq -e '(.results // .) | type == "array"' <<<"$body" >/dev/null \
    || fail "${JOURNEY:-?}" "search for $1 returned an unparseable body: $body"
  printf '%s\n' "$body"
}

# result_ids <search body>: every memory id in the result list, one per line.
result_ids() { jq -r '(.results // .)[] | .id' <<<"$1"; }

# hiking_id <search body>: the id of the memory holding the hiking fact, if any.
hiking_id() { jq -r '(.results // .)[] | select(.memory | test("hiking"; "i")) | .id' <<<"$1" | head -1; }

step_install() {
  step 1 "install with ci-values under the restricted namespace"
  assert_eq 1 "namespace pod security" restricted \
    "$(k get ns "$NS" -o jsonpath='{.metadata.labels.pod-security\.kubernetes\.io/enforce}')"
  k -n "$NS" get secret model-client >/dev/null || fail 1 "prerequisite Secret model-client missing"
  helm --kube-context "$CONTEXT" upgrade --install "$RELEASE" "$CHART_DIR" -n "$NS" \
    -f "$E2E_DIR/ci-values.yaml" || fail 1 "helm install failed"
  wait_ready || fail 1 "database or mem0 not ready within 5 minutes"
  local key
  key="$(k -n "$NS" get secret "$KEY_SECRET" -o jsonpath='{.data.api-key}' | base64 -d)" \
    || fail 1 "Secret $KEY_SECRET missing"
  [[ "$key" =~ ^[0-9a-f]{64}$ ]] || fail 1 "api-key is not 64 hex characters"
}

step_grant() {
  step 2 "the grant"
  local out
  out="$(k8s_get caller "/api/v1/namespaces/${NS}/secrets/${KEY_SECRET}")"
  assert_eq 2 "caller read of $KEY_SECRET" 200 "$(status_of "$out")"
  out="$(k8s_get stranger "/api/v1/namespaces/${NS}/secrets/${KEY_SECRET}")"
  assert_eq 2 "other service account read of $KEY_SECRET" 403 "$(status_of "$out")"
  local verdict
  verdict="$(k auth can-i get "secret/${KEY_SECRET}" -n "$NS" \
    --as "system:serviceaccount:${CALLER_NS}:other" || true)"
  assert_eq 2 "auth can-i for the other service account" no "$verdict"
}

step_memories() {
  JOURNEY=3
  step 3 "add, find, read and delete one person's memory"
  local out id
  out="$(api POST /memories "{\"messages\":[{\"role\":\"user\",\"content\":\"Hi, I am Alice.\"},{\"role\":\"user\",\"content\":\"$ALICE_FACT\"}],\"user_id\":\"alice\",\"agent_id\":\"helper\"}")"
  [ "$(status_of "$out")" = 200 ] || fail 3 "add returned $(status_of "$out"): $(body_of "$out")"
  local body
  body="$(search_json alice)"
  id="$(hiking_id "$body")"
  [ -n "$id" ] || fail 3 "search for alice did not return the fact"
  body="$(search_json bob)"
  assert_eq 3 "number of results for bob" 0 "$(jq '(.results // .) | length' <<<"$body")"
  out="$(api GET "/memories/$id")"
  assert_eq 3 "GET /memories/{id} status" 200 "$(status_of "$out")"
  assert_eq 3 "memory user_id" alice "$(body_of "$out" | jq -r '.user_id')"
  out="$(api DELETE "/memories/$id")"
  assert_eq 3 "DELETE /memories/{id} status" 200 "$(status_of "$out")"
  body="$(search_json alice)"
  if result_ids "$body" | grep -qx "$id"; then fail 3 "search still returns the deleted memory $id"; fi
  # Leave alice's fact in place for the restart step.
  out="$(api POST /memories "{\"messages\":[{\"role\":\"user\",\"content\":\"$ALICE_FACT\"}],\"user_id\":\"alice\",\"agent_id\":\"helper\"}")"
  [ "$(status_of "$out")" = 200 ] || fail 3 "re-add returned $(status_of "$out")"
  JOURNEY=
}

step_token() {
  JOURNEY=4
  step 4 "model calls carry a fresh service token; mem0 never holds the client secret"
  local calls n
  calls="$(fake_calls)"
  n="$(jq '[.[] | select(.path | test("^/v1/(chat/completions|embeddings)$"))] | length' <<<"$calls")"
  [ "$n" -gt 0 ] || fail 4 "the fake model saw no chat or embedding calls"
  assert_eq 4 "model calls with an invalid token" 0 \
    "$(jq '[.[] | select(.path | test("^/v1/")) | select(.valid | not)] | length' <<<"$calls")"
  local before after
  before="$(jq '[.[] | select(.path | test("^/v1/")) | .token] | unique | length' <<<"$calls")"
  echo "waiting past the 20 s token life"
  sleep 25
  local out
  out="$(api POST /memories '{"messages":[{"role":"user","content":"Alice also plays the cello on weekends"}],"user_id":"alice","agent_id":"helper"}')"
  [ "$(status_of "$out")" = 200 ] || fail 4 "add after the token expired returned $(status_of "$out"): $(body_of "$out")"
  calls="$(fake_calls)"
  after="$(jq '[.[] | select(.path | test("^/v1/")) | .token] | unique | length' <<<"$calls")"
  [ "$after" -gt "$before" ] || fail 4 "no second token seen after the first expired ($before then $after)"
  assert_eq 4 "model calls with an invalid token after refresh" 0 \
    "$(jq '[.[] | select(.path | test("^/v1/")) | select(.valid | not)] | length' <<<"$calls")"

  # The server container must not reference the client secret or its Secret.
  local pod spec
  pod="$(server_pod)"
  spec="$(k -n "$NS" get pod "$pod" -o json)"
  local server
  server="$(jq '.spec.containers[] | select(.name == "server")' <<<"$spec")"
  jq -e '.name' <<<"$server" >/dev/null || fail 4 "no server container in pod $pod"
  # shellcheck disable=SC2016
  [ "$(jq --argjson spec "$spec" '
      ([ .env[]? | select(.valueFrom.secretKeyRef.name == "model-client") ] | length)
      + ([ .envFrom[]? | select(.secretRef.name == "model-client") ] | length)
      + ([ .volumeMounts[]? as $m | $spec.spec.volumes[] | select(.name == $m.name and .secret.secretName == "model-client") ] | length)
      + ([ .env[]? | select(.value == "secret") ] | length)' <<<"$server")" = 0 ] \
    || fail 4 "the server container references model-client or the client secret value"
  if k -n "$NS" exec "$pod" -c server -- env | grep -q '=secret$'; then
    fail 4 "the server container environment holds the client secret value"
  fi
  JOURNEY=
}

step_restart() {
  JOURNEY=5
  step 5 "memories survive a restart of mem0"
  local before pod after
  before="$(result_ids "$(search_json alice)" | sort)"
  [ -n "$before" ] || fail 5 "no memory for alice before the restart"
  pod="$(server_pod)"
  k -n "$NS" delete pod "$pod" --wait=true
  k -n "$NS" rollout status "deploy/${FULL}" --timeout=300s || fail 5 "mem0 did not come back"
  k -n "$NS" wait --for=condition=Ready "pod/$(server_pod)" --timeout=300s
  after="$(result_ids "$(search_json alice)" | sort)"
  assert_eq 5 "alice's memories after the restart" "$before" "$after"
  JOURNEY=
}

step_allowlist() {
  JOURNEY=6
  step 6 "dangerous endpoints are closed even with the key"
  local uuid=00000000-0000-4000-8000-000000000000 spec method path body want out real
  # PUT and history are checked against a memory that exists, so a 404 can only be the allowlist's.
  out="$(api POST /memories "{\"messages\":[{\"role\":\"user\",\"content\":\"$ALICE_FACT\"}],\"user_id\":\"alice\",\"agent_id\":\"helper\"}")"
  [ "$(status_of "$out")" = 200 ] || fail 6 "add returned $(status_of "$out"): $(body_of "$out")"
  real="$(hiking_id "$(search_json alice)")"
  [ -n "$real" ] || fail 6 "no memory of alice's to test against"
  out="$(api GET "/memories/$real")"
  assert_eq 6 "GET /memories/{real id} (allowed route, existing memory)" 200 "$(status_of "$out")"
  out="$(api GET "/memories/$uuid")"
  echo "probe: mem0 answers GET /memories/<unknown id> on the allowed route with $(status_of "$out"): $(body_of "$out")"
  # method|path|body|expected status
  local cases=(
    "POST|/auth/register|{}|404"
    "POST|/configure|{}|404"
    "POST|/reset||404"
    "GET|/api-keys||404"
    "GET|/entities||404"
    "GET|/requests||404"
    "GET|/docs||404"
    "GET|/openapi.json||404"
    "POST|/generate-instructions|{}|404"
    "GET|/memories/$real/history||404"
    "PUT|/memories/$real|{}|404"
    "DELETE|/memories?user_id=alice||404"
    "GET|/memories||400"
    "GET|/memories?agent_id=helper||400"
    "GET|/memories?user_id=*||400"
    "GET|/memories?user_id=||400"
    "POST|/search|{\"query\":\"x\"}|400"
    "POST|/search|{\"query\":\"x\",\"filters\":{\"agent_id\":\"helper\"}}|400"
    "POST|/search|{\"query\":\"x\",\"filters\":{\"user_id\":\"*\"}}|400"
    "POST|/search|{\"query\":\"x\",\"filters\":{\"user_id\":\"\"}}|400"
    "POST|/search|{\"query\":\"x\",\"filters\":{\"OR\":[{\"user_id\":\"alice\"},{\"user_id\":\"bob\"}]}}|400"
    "POST|/memories|{\"messages\":[{\"role\":\"user\",\"content\":\"x\"}],\"user_id\":\"*\"}|400"
    "POST|/memories|{\"messages\":[{\"role\":\"user\",\"content\":\"x\"}]}|400"
  )
  for spec in "${cases[@]}"; do
    IFS='|' read -r method path body want <<<"$spec"
    out="$(api "$method" "$path" "$body")"
    assert_eq 6 "$method $path" "$want" "$(status_of "$out")"
  done
  # Allowed paths with no key.
  cases=(
    "GET|/memories?user_id=alice|"
    "GET|/memories/$uuid|"
    "POST|/search|{\"query\":\"x\",\"filters\":{\"user_id\":\"alice\"}}"
    "POST|/memories|{\"messages\":[{\"role\":\"user\",\"content\":\"x\"}],\"user_id\":\"alice\"}"
  )
  for spec in "${cases[@]}"; do
    IFS='|' read -r method path body <<<"$spec"
    out="$(api "$method" "$path" "$body" NOKEY)"
    assert_eq 6 "$method $path without a key" 401 "$(status_of "$out")"
  done
  JOURNEY=
}

step_upgrade() {
  step 9 "upgrade keeps the key"
  local key1 rv1 key2 rv2
  key1="$(k -n "$NS" get secret "$KEY_SECRET" -o jsonpath='{.data.api-key}')"
  rv1="$(k -n "$NS" get secret "$KEY_SECRET" -o jsonpath='{.metadata.resourceVersion}')"
  helm --kube-context "$CONTEXT" upgrade "$RELEASE" "$CHART_DIR" -n "$NS" \
    -f "$E2E_DIR/ci-values.yaml" || fail 9 "helm upgrade failed"
  wait_ready || fail 9 "not ready after the upgrade"
  key2="$(k -n "$NS" get secret "$KEY_SECRET" -o jsonpath='{.data.api-key}')"
  rv2="$(k -n "$NS" get secret "$KEY_SECRET" -o jsonpath='{.metadata.resourceVersion}')"
  assert_eq 9 "api-key after upgrade" "$key1" "$key2"
  assert_eq 9 "api-key resourceVersion after upgrade" "$rv1" "$rv2"
}

step_image() {
  step 11 "the running image is mem0 v2.2.1, hardened"
  local pod ver commit uid mounts err
  pod="$(server_pod)"
  [ -n "$pod" ] && [ "$pod" != null ] || fail 11 "no server pod"
  ver="$(k -n "$NS" exec "$pod" -c server -- pip show mem0ai 2>/dev/null)" || fail 11 "pip show mem0ai failed"
  assert_eq 11 "mem0ai version" 2.2.1 "$(sed -n 's/^Version: //p' <<<"$ver")"
  commit="$(k -n "$NS" exec "$pod" -c server -- cat /app/server/.mem0-commit)" || fail 11 "cannot read .mem0-commit"
  assert_eq 11 "/app/server/.mem0-commit" 94c3fe9f238f3dbf29c9ce98643bd71eb13077cd "$(tr -d '[:space:]' <<<"$commit")"
  uid="$(k -n "$NS" exec "$pod" -c server -- id -u)" || fail 11 "id -u failed"
  assert_eq 11 "server process uid" 10001 "$uid"
  assert_eq 11 "server readOnlyRootFilesystem in the pod spec" true     "$(k -n "$NS" get pod "$pod" -o json | jq -r '.spec.containers[] | select(.name == "server") | .securityContext.readOnlyRootFilesystem')"
  mounts="$(k -n "$NS" exec "$pod" -c server -- cat /proc/self/mounts)" || fail 11 "cannot read /proc/self/mounts"
  root_mount_is_ro <<<"$mounts" || fail 11 "the root mount is not read-only: $(awk '$2 == "/"' <<<"$mounts")"
  # The write must fail because the filesystem is read-only, not for lack of permission.
  if err="$(k -n "$NS" exec "$pod" -c server -- sh -c 'touch /app/.write-test' 2>&1)"; then
    fail 11 "writing to /app succeeded"
  fi
  grep -q 'Read-only file system' <<<"$err" || fail 11 "write to /app failed, but not with EROFS: $err"
  if helm --kube-context "$CONTEXT" template "$RELEASE" "$CHART_DIR" -n "$NS" \
      -f "$E2E_DIR/ci-values.yaml" | grep -E 'image:.*:latest'; then
    fail 11 "the rendered chart has a :latest image"
  fi
}

step_netpol() {
  step 7 "only named callers reach mem0, and only mem0 reaches its database"
  local db="${FULL}-db-rw.${NS}.svc"
  assert_eq 7 "outsider to mem0:8000" timeout "$(tcp_probe outsider outsider "${FULL}.${NS}.svc" 8000)"
  assert_eq 7 "stranger (tapas-sim, not the named pod) to mem0:8000" timeout \
    "$(tcp_probe "$CALLER_NS" stranger "${FULL}.${NS}.svc" 8000)"
  assert_eq 7 "caller to mem0:8000" connected "$(tcp_probe "$CALLER_NS" caller "${FULL}.${NS}.svc" 8000)"
  assert_eq 7 "caller to ${db}:5432" timeout "$(tcp_probe "$CALLER_NS" caller "$db" 5432)"
  assert_eq 7 "outsider to ${db}:5432" timeout "$(tcp_probe outsider outsider "$db" 5432)"
}

step_egress() {
  step 8 "mem0 works with egress closed except DNS, its database and the model endpoints"
  helm --kube-context "$CONTEXT" upgrade "$RELEASE" "$CHART_DIR" -n "$NS" \
    -f "$E2E_DIR/ci-values.yaml" -f "$E2E_DIR/egress-values.yaml" || fail 8 "helm upgrade with egress failed"
  wait_ready || fail 8 "not ready with egress closed"
  assert_eq 8 "server policy has an Egress policyType" Egress \
    "$(k -n "$NS" get networkpolicy "$FULL" -o jsonpath='{.spec.policyTypes[1]}')"
  step_memories
  step_token
  JOURNEY=8
  local pod
  pod="$(server_pod)"
  assert_eq 8 "MEM0_TELEMETRY in the server container" false \
    "$(k -n "$NS" exec "$pod" -c server -- printenv MEM0_TELEMETRY)"
  helm --kube-context "$CONTEXT" upgrade "$RELEASE" "$CHART_DIR" -n "$NS" \
    -f "$E2E_DIR/ci-values.yaml" || fail 8 "helm upgrade back to ci-values failed"
  wait_ready || fail 8 "not ready after reverting egress"
  JOURNEY=
}

ALL_STEPS=(install grant memories token restart allowlist upgrade image netpol egress)
STEPS=("$@")
[ ${#STEPS[@]} -gt 0 ] || STEPS=("${ALL_STEPS[@]}")
for s in "${STEPS[@]}"; do
  declare -F "step_$s" >/dev/null || { echo "unknown step: $s (known: ${ALL_STEPS[*]})" >&2; exit 2; }
done
for s in "${STEPS[@]}"; do "step_$s"; done
echo "E2E OK: ${STEPS[*]}"
