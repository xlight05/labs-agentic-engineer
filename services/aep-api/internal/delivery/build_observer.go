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

package delivery

import "context"

// BuildTerminal is one component's build reaching a terminal state at a
// commit. It is the whole fact the event plane needs: which component, at
// which commit, green or red, and why.
type BuildTerminal struct {
	OrgID     string
	ProjectID string
	Component string
	// CommitSHA is the commit the build was pinned to — for a webhook-driven
	// build, the merge SHA. It is half of the (component, SHA) key the
	// automatic re-trigger budget is counted on.
	CommitSHA string
	// RunName is the OpenChoreo WorkflowRun's name, for correlation in logs and
	// in the minted issue's prose.
	RunName string
	// Succeeded is the terminal verdict; Reason carries the failure output of a
	// red run (prose, never parsed).
	Succeeded bool
	Reason    string
}

// BuildTerminalObserver receives build terminals from whichever component
// watches OpenChoreo. It is the root port that keeps the WATCHER and the EVENT
// PLANE in peer sub-packages: the watcher lives with the executor whose
// package-private classification helpers it shares, and reports outwards
// through this interface rather than importing the event plane (which it may
// not — slices never import siblings).
//
// Implementations must be idempotent: the same terminal can be reported more
// than once (a watcher restart re-reads the same completed WorkflowRun).
type BuildTerminalObserver interface {
	OnBuildTerminal(ctx context.Context, ev BuildTerminal) error
}
