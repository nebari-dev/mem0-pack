#!/usr/bin/env bash
# Smoke test for the server image: bash image/smoke.sh IMAGE
# Runs migrate then the server against a throwaway pgvector Postgres and the fake model,
# adds and searches a memory, then checks the server refuses to start with a tampered or
# missing config_overrides row.
set -euo pipefail

IMAGE="${1:?usage: smoke.sh IMAGE}"
PGVECTOR_IMAGE="pgvector/pgvector:pg17@sha256:b513c48566e4777cd3370451400000c78eda6a08131d68f4c31d4a76f27bd8fd"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUFFIX="$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
NET="mem0-smoke-$SUFFIX"
PG="mem0-smoke-pg-$SUFFIX"
MODEL="mem0-smoke-model-$SUFFIX"
SERVER="mem0-smoke-server-$SUFFIX"
FAKE_IMAGE="mem0-smoke-fake-model:$SUFFIX"
# 2048 is above pgvector's 2000-dimension HNSW limit: this run proves index=auto copes with it.
# Set SMOKE_DIMS=384 to check the HNSW path.
DIMS="${SMOKE_DIMS:-2048}"
API_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"

cleanup() {
  docker rm -f "$SERVER" "$PG" "$MODEL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker rmi "$FAKE_IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "image $IMAGE not found"

docker network create "$NET" >/dev/null
docker build -q -t "$FAKE_IMAGE" "$HERE/../tests/e2e/fake-model" >/dev/null

# Postgres: role mem0 (no superuser) owns database mem0; the extension is created as superuser,
# as CloudNativePG's postInitApplicationSQL does.
docker run -d --name "$PG" --network "$NET" -e POSTGRES_PASSWORD=super "$PGVECTOR_IMAGE" >/dev/null
for _ in $(seq 60); do
  docker exec "$PG" pg_isready -U postgres -h 127.0.0.1 >/dev/null 2>&1 && break
  sleep 1
done
psql_super() { docker exec -i "$PG" psql -v ON_ERROR_STOP=1 -U postgres -h 127.0.0.1 "$@"; }
# the entrypoint restarts postgres once during init; wait until a real query works
for _ in $(seq 30); do psql_super -c 'select 1' >/dev/null 2>&1 && break; sleep 1; done
psql_super -c "CREATE ROLE mem0 LOGIN PASSWORD 'mem0pw' NOSUPERUSER" >/dev/null
psql_super -c "CREATE DATABASE mem0 OWNER mem0" >/dev/null
psql_super -d mem0 -c "CREATE EXTENSION vector" >/dev/null

docker run -d --name "$MODEL" --network "$NET" -e REQUIRE_TOKEN=false -e DIMS="$DIMS" "$FAKE_IMAGE" >/dev/null

MODEL_URL="http://$MODEL:8080/v1"
env_args=(
  -e POSTGRES_HOST="$PG" -e POSTGRES_PORT=5432 -e POSTGRES_DB=mem0 -e APP_DB_NAME=mem0
  -e POSTGRES_USER=mem0 -e POSTGRES_PASSWORD=mem0pw
  -e ADMIN_API_KEY="$API_KEY" -e OPENAI_API_KEY=placeholder -e OPENAI_BASE_URL="$MODEL_URL"
  -e MEM0PACK_MODEL_BASE_URL="$MODEL_URL" -e MEM0PACK_LLM_MODEL=fake-llm
  -e MEM0PACK_EMBEDDING_MODEL=fake-embed -e MEM0PACK_EMBEDDING_DIMS="$DIMS"
)
# tmpfs is root-owned by default; in a pod the emptyDir gets its group from fsGroup
ro_args=(--read-only --tmpfs /tmp --tmpfs /var/lib/mem0:uid=10001,gid=10001)

echo "== migrate"
docker run --rm --name "$SERVER-migrate" --network "$NET" "${ro_args[@]}" "${env_args[@]}" \
  --entrypoint python "$IMAGE" -m mem0pack.migrate

echo "== serve"
docker run -d --name "$SERVER" --network "$NET" -p 127.0.0.1::8000 "${ro_args[@]}" "${env_args[@]}" \
  "$IMAGE" >/dev/null
PORT="$(docker port "$SERVER" 8000/tcp | head -n1 | sed 's/.*://')"
BASE="http://127.0.0.1:$PORT"

code=000
for _ in $(seq 60); do
  code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)"
  [ "$code" = 200 ] && break
  docker ps -q -f name="^$SERVER$" | grep -q . || { docker logs "$SERVER" >&2; fail "server exited"; }
  sleep 1
done
[ "$code" = 200 ] || { docker logs "$SERVER" >&2; fail "/healthz returned $code"; }
echo "healthz 200"

echo "== add and search"
add="$(curl -s -w '\n%{http_code}' -X POST "$BASE/memories" -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"I love hiking in the Alps every summer"}],"user_id":"alice"}')"
[ "$(echo "$add" | tail -n1)" = 200 ] || { docker logs "$SERVER" >&2; fail "add failed: $add"; }
found="$(curl -s -X POST "$BASE/search" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' \
  -d '{"query":"hiking in the Alps","filters":{"user_id":"alice"}}')"
echo "$found" | grep -q 'hiking' || { docker logs "$SERVER" >&2; fail "search did not find the memory: $found"; }
echo "memory added and found"

echo "== index"
hnsw_idx="$(psql_super -d mem0 -tA -c "SELECT count(*) FROM pg_indexes WHERE indexdef LIKE '%USING hnsw%'")"
if [ "$DIMS" -gt 2000 ]; then want=0; else want=1; fi
[ "$hnsw_idx" = "$want" ] || fail "expected $want hnsw indexes at $DIMS dimensions, found $hnsw_idx"
echo "$hnsw_idx hnsw index(es) at $DIMS dimensions"

echo "== allowlist"
status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
expect_status() {
  local want="$1" got; shift
  got="$(status "$@")"
  [ "$got" = "$want" ] || { docker logs "$SERVER" >&2; fail "expected $want, got $got: $*"; }
}
for path in /auth/register /configure /reset; do
  expect_status 404 -X POST "$BASE$path" -H "X-API-Key: $API_KEY" \
    -H 'Content-Type: application/json' -d '{}'
done
expect_status 400 "$BASE/memories?user_id=*" -H "X-API-Key: $API_KEY"
expect_status 401 "$BASE/memories?user_id=alice"
echo "allowlist refuses unlisted paths, wildcards and keyless calls"

docker rm -f "$SERVER" >/dev/null

run_server_expect_exit() {
  local label="$1" want="$2" out rc=0
  out="$(timeout 120 docker run --rm --name "$SERVER-$label" --network "$NET" "${ro_args[@]}" \
    "${env_args[@]}" "$IMAGE" 2>&1)" || rc=$?
  echo "-- $label: exit $rc"
  echo "$out" | tail -n 8
  [ "$rc" -ne 0 ] && [ "$rc" -ne 124 ] || fail "$label: server should exit non-zero (rc=$rc)"
  # the config row is checked before mem0's main is imported (its import logs "Using psycopg")
  if echo "$out" | grep -q 'Using psycopg'; then fail "$label: mem0 was imported before the check"; fi
  echo "$out" | grep -q "$want" || fail "$label: expected message '$want'"
}

echo "== tampered config_overrides"
psql_super -d mem0 -c "UPDATE settings SET value='{}' WHERE key='config_overrides'" >/dev/null
run_server_expect_exit tampered 'config_overrides differs from pack settings'

echo "== missing config_overrides"
psql_super -d mem0 -c "DELETE FROM settings WHERE key='config_overrides'" >/dev/null
run_server_expect_exit missing 'config_overrides is missing'

echo "SMOKE OK"
