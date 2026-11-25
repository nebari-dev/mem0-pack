#!/usr/bin/env bash
# Fail if software-pack-template material is still wired into CI or dev tooling.
# Scope: .github/ (this script excluded) and dev/. Invoke with bash.
set -euo pipefail

cd "$(dirname "$0")/../.."

pattern='examples/auth-fastapi|my-pack|nebari-software-pack-template|building-a-software-pack'
self=".github/scripts/check-template-leftovers.sh"
status=0

hits=$(grep -rnE "$pattern" .github dev --exclude-dir=.cache || true)
hits=$(printf '%s\n' "$hits" | grep -v "^${self}:" | grep -v '^$' || true)
if [ -n "$hits" ]; then
    echo "Template leftovers found:"
    echo "$hits"
    status=1
fi

release_if=$(uv run --no-project --quiet --with pyyaml python - <<'PY'
import yaml

with open(".github/workflows/release.yaml") as f:
    doc = yaml.safe_load(f)
print(repr(doc["jobs"]["release"].get("if")))
PY
)
if [ "$release_if" != "False" ]; then
    echo "release.yaml: jobs.release.if must be false until the first release is cut (found: $release_if)"
    status=1
fi

if [ "$status" -eq 0 ]; then
    echo "No template leftovers."
fi
exit "$status"
