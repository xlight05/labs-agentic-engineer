#!/usr/bin/env bash
# Prototype asset for wso2/labs-agentic-engineer#279.
# Simulates the agent run finishing: sub-issues close in dependency order
# (in the real flow the merged PR's Resolves list does this). Between closes,
# reads the signals the backend would key on: the freed sub-issue's
# issue_dependencies_summary and the parent's sub_issues_summary/state.
set -euo pipefail
R="${SANDBOX_REPO:-xlight05/aep-issue-graph-sandbox}"
source "${1:-graph.env}"

close() { gh api -X PATCH "repos/$R/issues/$1" -f state=closed -f state_reason=completed --jq '"closed #\(.number)"'; sleep 1; }
dep_summary() { gh api "repos/$R/issues/$1" --jq '"#\(.number) \(.state) deps=\(.issue_dependencies_summary)"'; }
parent_summary() { gh api "repos/$R/issues/$P_NUM" --jq '"parent #\(.number) \(.state) subs=\(.sub_issues_summary)"'; }

echo "== before: S2 blocked"; dep_summary "$S2_NUM"
close "$S1_NUM"
echo "== after closing S1: blocked_by should drop to 0 (open-only), total stays"; dep_summary "$S2_NUM"; dep_summary "$S3_NUM"
close "$S2_NUM"; close "$S3_NUM"
echo "== after closing S2+S3:"; dep_summary "$S4_NUM"
close "$S4_NUM"
echo "== all subs closed — does the parent auto-close? (research says no)"; parent_summary
