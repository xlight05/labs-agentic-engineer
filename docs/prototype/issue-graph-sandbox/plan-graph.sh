#!/usr/bin/env bash
# Prototype asset for wso2/labs-agentic-engineer#279 (wayfinder map #272).
#
# Scripts the planner's output shape against a sandbox repo using ONLY the
# calls the aep-api planner backend would make (REST via gh api):
#   1 parent + 4 sub-issues (diamond dependency graph)
#   - sub-issue links:   POST /repos/{r}/issues/{parent}/sub_issues   (takes DB id)
#   - blocked-by edges:  POST /repos/{r}/issues/{n}/dependencies/blocked_by (takes DB id)
#
# Per research (#278): edges cannot be attached at creation; content-generating
# calls are spaced >=1s to respect secondary rate limits (80/min, 500/hr).
set -euo pipefail

R="${SANDBOX_REPO:-xlight05/aep-issue-graph-sandbox}"
OUT="${OUT:-graph.env}"

pace() { sleep 1; }

create_issue() { # $1=title $2=body $3...=labels -> "number id"
  local args=(-f "title=$1" -f "body=$2"); shift 2
  local l; for l in "$@"; do args+=(-f "labels[]=$l"); done
  gh api -X POST "repos/$R/issues" "${args[@]}" --jq '"\(.number) \(.id)"'
}

echo "== creating parent"
read -r P_NUM P_ID < <(create_issue \
  "Orders: capture and list customer orders" \
  "$(cat <<'EOF'
## Goal

Ship the orders slice of spec v1: a customer can place an order and see it in the console.

## Delivery

One coding agent works every sub-issue below on a single feature branch and ships one agent-validated PR. Sub-issue dependencies define build order; the graph below is the plan.
EOF
)" aep "aep:spec/v1")
echo "parent: #$P_NUM (id $P_ID)"; pace

echo "== creating sub-issues"
read -r S1_NUM S1_ID < <(create_issue \
  "Order domain model and migrations" \
  "Add the \`orders\` table, Go domain model, and repository. No API surface yet." aep)
echo "S1: #$S1_NUM"; pace
read -r S2_NUM S2_ID < <(create_issue \
  "POST /orders endpoint" \
  "Contract-first endpoint to create an order. Depends on the domain model." aep)
echo "S2: #$S2_NUM"; pace
read -r S3_NUM S3_ID < <(create_issue \
  "Order status webhook consumer" \
  "Consume payment-status webhooks and update order state. Depends on the domain model." aep)
echo "S3: #$S3_NUM"; pace
read -r S4_NUM S4_ID < <(create_issue \
  "Console: orders list page" \
  "List orders with live status in the console. Depends on the endpoint and the status consumer." aep)
echo "S4: #$S4_NUM"; pace

echo "== linking sub-issues to parent (takes DB id, not number)"
for id in "$S1_ID" "$S2_ID" "$S3_ID" "$S4_ID"; do
  gh api -X POST "repos/$R/issues/$P_NUM/sub_issues" -F sub_issue_id="$id" --jq .number
  pace
done

echo "== wiring blocked-by edges (diamond: S2,S3 <- S1; S4 <- S2,S3)"
gh api -X POST "repos/$R/issues/$S2_NUM/dependencies/blocked_by" -F issue_id="$S1_ID" --jq .number; pace
gh api -X POST "repos/$R/issues/$S3_NUM/dependencies/blocked_by" -F issue_id="$S1_ID" --jq .number; pace
gh api -X POST "repos/$R/issues/$S4_NUM/dependencies/blocked_by" -F issue_id="$S2_ID" --jq .number; pace
gh api -X POST "repos/$R/issues/$S4_NUM/dependencies/blocked_by" -F issue_id="$S3_ID" --jq .number; pace

cat > "$OUT" <<EOF
P_NUM=$P_NUM P_ID=$P_ID
S1_NUM=$S1_NUM S1_ID=$S1_ID
S2_NUM=$S2_NUM S2_ID=$S2_ID
S3_NUM=$S3_NUM S3_ID=$S3_ID
S4_NUM=$S4_NUM S4_ID=$S4_ID
EOF
echo "== done; graph in $OUT (13 content-generating calls: 5 issues + 4 links + 4 edges)"
