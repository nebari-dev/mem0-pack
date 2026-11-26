#!/usr/bin/env bash
# Smoke test for the tools image: bash tools/smoke.sh IMAGE
# Runs the token proxy against the fake model (token TTL 20 s, token required), calls a
# model endpoint through the proxy, waits past the TTL, calls again, then asserts the fake
# model saw two distinct valid tokens. The proxy listens on loopback only, so calls are made
# from inside its network namespace.
set -euo pipefail

IMAGE="${1:?usage: smoke.sh IMAGE}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUFFIX="$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
NET="tools-smoke-$SUFFIX"
MODEL="tools-smoke-model-$SUFFIX"
PROXY="tools-smoke-proxy-$SUFFIX"
FAKE_IMAGE="tools-smoke-fake-model:$SUFFIX"
CREDS="$(mktemp -d)"

cleanup() {
  docker rm -f "$PROXY" "$MODEL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker rmi "$FAKE_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$CREDS"
}
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "image $IMAGE not found"

docker network create "$NET" >/dev/null
docker build -q -t "$FAKE_IMAGE" "$HERE/../tests/e2e/fake-model" >/dev/null

docker run -d --name "$MODEL" --network "$NET" \
  -e TOKEN_TTL=20 -e REQUIRE_TOKEN=true -e CLIENT_ID=mem0 -e CLIENT_SECRET=secret \
  "$FAKE_IMAGE" >/dev/null

# The tools image runs as 65532, so the credential files must be world-readable.
chmod 755 "$CREDS"
printf 'mem0' >"$CREDS/client-id"
printf 'secret' >"$CREDS/client-secret"
chmod 644 "$CREDS/client-id" "$CREDS/client-secret"

# pycall METHOD URL [BODY]: run python inside the proxy's network namespace (the fake-model
# image is python-based); print the response body, exit nonzero on a non-2xx status.
pycall() {
  docker run --rm --network "container:$PROXY" --entrypoint python "$FAKE_IMAGE" -c '
import sys, urllib.request
method, url = sys.argv[1], sys.argv[2]
body = sys.argv[3].encode() if len(sys.argv) > 3 else None
req = urllib.request.Request(url, data=body, method=method, headers={"Content-Type": "application/json"})
sys.stdout.write(urllib.request.urlopen(req, timeout=15).read().decode())
' "$@"
}

for _ in $(seq 30); do
  docker run --rm --network "$NET" --entrypoint python "$FAKE_IMAGE" -c \
    "import urllib.request; urllib.request.urlopen('http://$MODEL:8080/healthz', timeout=2)" \
    >/dev/null 2>&1 && break
  sleep 1
done

docker run -d --name "$PROXY" --network "$NET" \
  -v "$CREDS:/creds:ro" \
  "$IMAGE" token-proxy \
  -listen 127.0.0.1:8081 \
  -upstream "http://$MODEL:8080/v1" \
  -token-url "http://$MODEL:8080/token" \
  -client-id-file /creds/client-id \
  -client-secret-file /creds/client-secret >/dev/null

CHAT='{"model":"m","messages":[{"role":"user","content":"hello"}]}'
for _ in $(seq 30); do
  pycall POST http://127.0.0.1:8081/v1/chat/completions "$CHAT" >/dev/null 2>&1 && break
  sleep 1
done
pycall POST http://127.0.0.1:8081/v1/chat/completions "$CHAT" | grep -q '"chat.completion"' \
  || fail "first chat call through the proxy did not succeed"

echo "waiting 25 s for the first token to expire"
sleep 25
pycall POST http://127.0.0.1:8081/v1/chat/completions "$CHAT" | grep -q '"chat.completion"' \
  || fail "chat call after token expiry did not succeed"

CALLS="$(docker run --rm --network "$NET" --entrypoint python "$FAKE_IMAGE" -c \
  "import urllib.request; print(urllib.request.urlopen('http://$MODEL:8080/calls', timeout=5).read().decode())")"

printf '%s' "$CALLS" | python3 -c '
import json, sys
calls = [c for c in json.load(sys.stdin) if c["path"] == "/v1/chat/completions"]
valid = [c for c in calls if c["valid"] and c["token"]]
tokens = {c["token"] for c in valid}
if len(valid) < 2 or len(tokens) < 2:
    sys.exit("expected >=2 valid calls with distinct tokens, got %d valid, %d distinct" % (len(valid), len(tokens)))
' || fail "fake model did not record two distinct valid tokens: $CALLS"

echo "PROXY OK"
