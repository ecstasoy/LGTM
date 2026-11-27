#!/usr/bin/env bash
# Decides whether deploy.yml ships HEAD_SHA, by diffing it against the commit Fly is running now.
# Usage: deploy-decision.sh <head_sha> <flyctl image show --json output file> <github event name>
# Prints `deploy=true` or `deploy=false` on stdout (for $GITHUB_OUTPUT) and the reason on stderr.
set -euo pipefail

head_sha=$1
image_json=$2
event=$3
relevant='^(backend/|frontend/|docs/|\.dockerignore$|\.github/workflows/deploy\.yml$|\.github/scripts/deploy-decision\.sh$)'

decide() { echo "deploy=$1"; echo "$2" >&2; exit 0; }

[ "$event" = workflow_dispatch ] && decide true "manual dispatch"

# Our own label first; flyctl's GH_SHA is only right for deploys made by the old push trigger
# Unreadable output counts as "unknown", and only a full sha is trusted (a ref name like HEAD would also resolve)
deployed=$(jq -r '.[0].Labels | (fromjson? // {}) | .LGTM_DEPLOY_SHA // .GH_SHA // empty' "$image_json" 2>/dev/null || true)

if ! [[ "$deployed" =~ ^[0-9a-f]{40}$ ]] || ! git cat-file -e "${deployed}^{commit}" 2>/dev/null; then
    decide true "deployed commit unknown (${deployed:-none}); deploying"
fi
if git merge-base --is-ancestor "$head_sha" "$deployed"; then
    decide false "$head_sha is already part of the deployed $deployed; skipping"
fi
# Diff from what is live, so changes from pending runs that a newer run cancelled still ship
if git diff --name-only "$deployed" "$head_sha" | grep -qE "$relevant"; then
    decide true "deploy-relevant changes since $deployed"
fi
decide false "no deploy-relevant changes since $deployed; skipping"
