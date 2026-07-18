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
	"context"
	"errors"
	"testing"
)

// fakeBuildProvisioner records the ProvisionForBuild delegation.
type fakeBuildProvisioner struct {
	calls     int
	gotInputs []ProvisionInput
	fails     []ProvisionFailure
	err       error
}

func (f *fakeBuildProvisioner) ProvisionForBuild(_ context.Context, _, _, _ string, inputs []ProvisionInput) ([]ProvisionFailure, error) {
	f.calls++
	f.gotInputs = inputs
	return f.fails, f.err
}

// TestProvisionDependencies_Delegates: the activity is a thin adapter — it
// forwards the inputs to the BuildProvisioner port and returns its failures.
func TestProvisionDependencies_Delegates(t *testing.T) {
	fp := &fakeBuildProvisioner{fails: []ProvisionFailure{{Component: "o", Dependency: "stripe", Reason: "bad"}}}
	acts := NewActivities(Deps{Provisioner: fp})

	got, err := acts.ProvisionDependencies(context.Background(), ProvisionDepsInput{
		OrgID: "acme", ProjectID: "shop", Tag: "v3",
		Inputs: []ProvisionInput{{Component: "o", Dependency: "stripe", Kind: "external-config"}},
	})
	if err != nil {
		t.Fatalf("ProvisionDependencies: %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("provisioner must be called once, got %d", fp.calls)
	}
	if len(fp.gotInputs) != 1 || fp.gotInputs[0].Dependency != "stripe" {
		t.Fatalf("inputs must be forwarded, got %+v", fp.gotInputs)
	}
	if len(got) != 1 || got[0].Dependency != "stripe" {
		t.Fatalf("failures must be returned to the workflow, got %+v", got)
	}
}

// TestProvisionDependencies_NoInputsNoProvisionerSkips: with nothing to
// provision the activity is a no-op even when the provisioner is unwired (the
// interim state before the app-root adapter lands) — a build that needs no
// provisioning must not fail.
func TestProvisionDependencies_NoInputsNoProvisionerSkips(t *testing.T) {
	acts := NewActivities(Deps{})
	got, err := acts.ProvisionDependencies(context.Background(), ProvisionDepsInput{OrgID: "a", ProjectID: "p", Tag: "v1"})
	if err != nil {
		t.Fatalf("empty provisioning must be a no-op, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("no inputs → no failures, got %+v", got)
	}
}

// TestProvisionDependencies_NoInputsWiredReconciles: with a provisioner wired,
// the activity runs even with NO drawer inputs — ProvisionForBuild reconciles
// any provision gate whose dep is already Ready but was left un-completed by a
// prior build (self-heal — issue #164). The old short-circuit skipped this.
func TestProvisionDependencies_NoInputsWiredReconciles(t *testing.T) {
	fp := &fakeBuildProvisioner{}
	acts := NewActivities(Deps{Provisioner: fp})
	if _, err := acts.ProvisionDependencies(context.Background(), ProvisionDepsInput{OrgID: "a", ProjectID: "p", Tag: "v1"}); err != nil {
		t.Fatalf("no-inputs reconcile must not error, got %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("provisioner must be called once for reconciliation even with no inputs, got %d", fp.calls)
	}
}

// TestProvisionDependencies_PropagatesError: an infra error from the port
// surfaces as the activity error (the workflow retries / fails the run).
func TestProvisionDependencies_PropagatesError(t *testing.T) {
	fp := &fakeBuildProvisioner{err: errors.New("infra down")}
	acts := NewActivities(Deps{Provisioner: fp})
	_, err := acts.ProvisionDependencies(context.Background(), ProvisionDepsInput{
		Inputs: []ProvisionInput{{Dependency: "x", Kind: "external-config"}},
	})
	if err == nil {
		t.Fatalf("port error must surface as the activity error")
	}
}

// TestProvisionDependencies_InputsButUnwiredErrors: inputs present but no
// provisioner is a real misconfiguration, surfaced (not silently dropped).
func TestProvisionDependencies_InputsButUnwiredErrors(t *testing.T) {
	acts := NewActivities(Deps{})
	_, err := acts.ProvisionDependencies(context.Background(), ProvisionDepsInput{
		Inputs: []ProvisionInput{{Dependency: "x", Kind: "external-config"}},
	})
	if err == nil {
		t.Fatalf("inputs with no provisioner wired must error")
	}
}
