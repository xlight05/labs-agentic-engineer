#!/usr/bin/env bash
# Prototype asset for wso2/labs-agentic-engineer#279.
# Drains the sandbox webhook's recorded deliveries into payloads/ as
# <deliveryId>-<event>.<action>.json. GitHub stores the full request payload
# for every delivery attempt — even failed ones — so a dead-drop URL plus this
# script captures real payloads with no listener infrastructure.
set -euo pipefail

R="${SANDBOX_REPO:-xlight05/aep-issue-graph-sandbox}"
HOOK="${HOOK_ID:?set HOOK_ID}"
DIR="${DIR:-payloads}"
mkdir -p "$DIR"

gh api "repos/$R/hooks/$HOOK/deliveries" --paginate \
  --jq '.[] | "\(.id) \(.event) \(.action // "none")"' |
while read -r id event action; do
  f="$DIR/$id-$event.$action.json"
  [ -e "$f" ] && continue
  gh api "repos/$R/hooks/$HOOK/deliveries/$id" --jq '.request.payload' > "$f"
  echo "captured $f"
done
