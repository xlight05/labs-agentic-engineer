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

package devflow

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

// Gate names — every human-in-the-loop pause point in the workflows.
// Each gate runs in auto mode by default (no pause); a run started with
// GateConfig.Auto[name] = false pauses there until a SigGateDecision.
// NOTE: the build endpoint always starts runs with the zero GateConfig (all
// auto) and the gate-decide endpoint left the public surface with the devflow
// routes, so only auto mode is reachable today; the gatekeeper machinery stays
// for a future HITL surface.
const (
	// DevFlowWorkflow gates.
	GatePlan     = "plan"     // before task planning
	GateValidate = "validate" // before the validation step
	GateComplete = "complete" // before the workflow completes

	// TaskFlowWorkflow gates.
	GateStartCoding = "start-coding" // before dispatching the coding agent
	GateMergePR     = "merge-pr"     // before auto-merging the task PR
	GateRetryBuild  = "retry-build"  // after a failed build: retry or fail
)

// GateConfig selects auto vs human-in-the-loop per gate for one run.
// A gate absent from Auto runs in auto mode (the default): awaitGate returns
// immediately. Auto[name] = false makes the gate manual: the workflow pauses,
// surfaces the gate in its status query, and waits for a SigGateDecision.
type GateConfig struct {
	Auto map[string]bool `json:"auto,omitempty"`
	// ApprovalTimeoutSeconds bounds a manual gate's wait; 0 means wait
	// indefinitely (the workflow run timeout still applies). A timeout is
	// treated as a rejection.
	ApprovalTimeoutSeconds int `json:"approvalTimeoutSeconds,omitempty"`
}

// IsAuto reports whether the named gate runs without a human pause.
func (c GateConfig) IsAuto(gate string) bool {
	if c.Auto == nil {
		return true
	}
	auto, ok := c.Auto[gate]
	return !ok || auto
}

// gateKeeper multiplexes the single SigGateDecision channel to per-gate
// waiters. One keeper is created per workflow run; awaitGate consumes
// decisions for its gate and ignores (drops) decisions for gates nobody is
// waiting on — the API layer validates gate names, so a mismatch is a stale
// or duplicate click, not a lost approval.
type gateKeeper struct {
	cfg        GateConfig
	setPending func(string) // updates the status query's PendingGate
}

func newGateKeeper(cfg GateConfig, setPending func(string)) *gateKeeper {
	return &gateKeeper{cfg: cfg, setPending: setPending}
}

// await blocks at a gate. Auto mode returns approved immediately. Manual mode
// publishes the pending gate, then waits for a SigGateDecision naming this
// gate (decisions for other gates are dropped), or the configured approval
// timeout (treated as a rejection).
func (g *gateKeeper) await(ctx workflow.Context, gate string) (approved bool, decision GateDecisionSignal) {
	if g.cfg.IsAuto(gate) {
		return true, GateDecisionSignal{Gate: gate, Approve: true}
	}
	g.setPending(gate)
	defer g.setPending("")

	ch := workflow.GetSignalChannel(ctx, SigGateDecision)
	var timerCtx workflow.Context
	var cancelTimer workflow.CancelFunc
	var timer workflow.Future
	if g.cfg.ApprovalTimeoutSeconds > 0 {
		timerCtx, cancelTimer = workflow.WithCancel(ctx)
		timer = workflow.NewTimer(timerCtx, time.Duration(g.cfg.ApprovalTimeoutSeconds)*time.Second)
		defer cancelTimer()
	}

	for {
		var got GateDecisionSignal
		timedOut := false
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, &got)
		})
		if timer != nil {
			sel.AddFuture(timer, func(workflow.Future) { timedOut = true })
		}
		sel.Select(ctx)
		if timedOut {
			return false, GateDecisionSignal{Gate: gate, Approve: false, Note: "approval timeout"}
		}
		if got.Gate != gate {
			continue // decision for another gate — drop and keep waiting
		}
		return got.Approve, got
	}
}
