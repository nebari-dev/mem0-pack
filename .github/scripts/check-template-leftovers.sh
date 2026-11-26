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

if [ "$status" -eq 0 ]; then
    echo "No template leftovers."
fi
exit "$status"
