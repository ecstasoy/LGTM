#!/usr/bin/env bash
# Tests deploy-decision.sh against a throwaway git repo; run: bash .github/scripts/deploy-decision_test.sh
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/deploy-decision.sh"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cd "$tmp" && git init -q && git config user.email t@t && git config user.name t
commit() { mkdir -p "$(dirname "$1")"; echo "$2" >> "$1"; git add -A; git commit -qm "$2"; git rev-parse HEAD; }
A=$(commit backend/a.go a)      # deployed
B=$(commit backend/b.go b)      # backend change whose deploy got cancelled while pending
C=$(commit README.md c)         # README-only tip
D=$(commit README.md d)         # README-only again, after C is deployed
img() { printf '[{"Tag":"deployment-x","Labels":%s}]' "$(jq -Rn --arg l "$1" '$l')" > "$tmp/img.json"; }
fail=0
expect() { # name want head [event]
  got=$(bash "$script" "$3" "$tmp/img.json" "${4:-workflow_run}" 2>/dev/null || true)
  if [ "$got" = "deploy=$2" ]; then echo "ok   $1"; else echo "FAIL $1: want deploy=$2, got '$got'"; fail=1; fi
}
img "{\"LGTM_DEPLOY_SHA\":\"$A\"}"
expect "README tip still ships a cancelled backend change" true "$C"
img "{\"LGTM_DEPLOY_SHA\":\"$C\"}"
expect "README-only since last deploy skips" false "$D"
expect "re-run of an older CI never rolls back" false "$B"
expect "re-run of the deployed commit skips" false "$C"
img "{\"GH_ACTION_NAME\":\"__run\",\"GH_EVENT_NAME\":\"push\",\"GH_SHA\":\"$A\"}"
expect "legacy GH_SHA label is used when ours is missing" true "$B"
img "{\"GH_SHA\":\"$C\"}"
expect "legacy label: README-only skips" false "$D"
img "{}"
expect "unknown deployed commit deploys" true "$D"
printf '[{"Tag":"x","Labels":null}]' > "$tmp/img.json"
expect "null labels deploys" true "$D"
img "{\"LGTM_DEPLOY_SHA\":\"0000000000000000000000000000000000000000\"}"
expect "deployed commit missing from history deploys" true "$D"
img "{\"LGTM_DEPLOY_SHA\":\"--output=/tmp/x\"}"
expect "a label that is not a sha deploys" true "$D"
img '{"LGTM_DEPLOY_SHA":"HEAD"}'
expect "a ref name that resolves is still not a sha, so it deploys" true "$D"
printf 'not json' > "$tmp/img.json"
expect "unparseable image output deploys" true "$D"
: > "$tmp/img.json"
expect "empty image output deploys" true "$D"
img "{\"LGTM_DEPLOY_SHA\":\"$D\"}"
expect "workflow_dispatch always deploys" true "$D" workflow_dispatch
exit $fail
