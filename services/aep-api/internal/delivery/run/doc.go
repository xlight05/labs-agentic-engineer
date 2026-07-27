// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Package run is the MILESTONE RUN SUPERVISOR: one Temporal workflow that
// works the open issues in one milestone until the milestone settles.
//
// There is exactly ONE run species. A spec build and an incident adoption are
// the same loop over the same milestone model, differing only in origin — and
// in whether the run gets a validation cycle. The workflow is therefore keyed
// by the milestone (`run-<org>-<project>-<milestoneNumber>`) and its id is
// REUSED after a terminal run, because a milestone sees sequential runs across
// its life.
//
// The loop, and the whole of this package:
//
//	WAIT ──► dispatch the coding agent ──► PR opened ──► auto-merge ──► builds + deploy
//	 ▲        (prompt = milestone reference only)                            │
//	 │  all green, open issues remain ─────────────► next cycle (re-WAIT)    │
//	 │  red after the one automatic re-trigger ─► FIX issue ─► next cycle    │
//	 │  merge conflict ─────────────────────────► CONFLICT issue ─► next     │
//	 │  deployed-green + empty working set + spec run ─► VALIDATION cycle    │
//	 └─ settle: zero open non-gate `aep` issues + terminal validation
//	            └─► close the milestone · run row → succeeded
//	    budgets exhausted / no progress / cancel ─► failed | cancelled
//
// # Division of labour
//
// The supervisor DECIDES; it detects nothing. Every fact about the world
// arrives from `delivery/eventcore` as a run signal, and the supervisor
// re-derives that fact from GROUND TRUTH before acting on it — a signal is a
// wake-up, never evidence. That is what makes a lost delivery cost latency
// instead of correctness, and it is why the wait state can be unbounded: the
// cycle-boundary poll and the reconcile sweep both re-read the milestone.
//
// The two packages never import each other. They share the milestone
// vocabulary (labels, run signals, the run and cycle rows, the build-fan-out
// naming) through the delivery ROOT, and they reach each other only through
// ports: `eventcore.RunSignaler`/`RunStarter` inbound, and nothing outbound.
//
// # What this package does not hold
//
// No gorm (persistence is reached through ports onto the root repositories),
// no GitHub client, no Kubernetes, and no issue-body parsing. The agent
// dispatch is the root `delivery.MilestoneDispatcher` port, satisfied by the
// coding agent — the supervisor hands over a milestone reference and learns
// what happened from webhooks.
package run
