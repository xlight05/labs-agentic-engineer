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

package ops_test

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/ops"
)

// The wire-shape golden for the P1 entity move.
//
// Before P1 the gorm model WAS the wire type (the contract carried
// `x-go-type: models.RcaAgentReport`). P1 split them, which puts a mapping
// between the domain and the wire — the exact place a field silently changes
// presence. These tests pin the shape against what the aliased model produced.
//
// The one deliberate schema edit was marking `dispatched` required. It is an
// ACCURACY fix, not a wire change: the model had `json:"dispatched"` with no
// omitempty, so the server has always sent it. Leaving it optional would have
// generated `omitempty` and made `dispatched:false` vanish — a real regression
// for the console's Coding Handover stage.

// TestWireShape_MinimalReport pins the keys a report with only its required
// fields set must carry. A key appearing or vanishing here is a wire change.
func TestWireShape_MinimalReport(t *testing.T) {
	created := time.Date(2026, 7, 17, 10, 30, 0, 0, time.UTC)
	body, err := json.Marshal(ops.ToWire(ops.RcaAgentReport{
		ID:             "r1",
		OrgID:          "acme", // must NOT appear on the wire
		Project:        "proj",
		Title:          "Checkout 500s",
		Summary:        "Spike in 500s",
		Classification: "code-level",
		Diagnosis:      "npe",
		CreatedAt:      created,
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Exactly the required fields + dispatched/deployed, which the server has
	// always sent unconditionally. Optional-and-absent fields must stay absent.
	want := []string{
		"classification", "createdAt", "deployed", "diagnosis", "dispatched",
		"id", "project", "summary", "title",
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != len(want) {
		t.Fatalf("wire keys = %v\n            want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("wire keys = %v\n            want %v", keys, want)
		}
	}

	// dispatched:false must be PRESENT, not omitted — the regression the
	// contract's `required` edit prevents.
	if v, ok := got["dispatched"]; !ok || v != false {
		t.Errorf("dispatched = %v (present=%v), want false and present", v, ok)
	}
}

// TestWireShape_OrgIDNeverLeaks is the dividend of splitting the types: the
// tenant key exists on the domain entity and simply has nowhere to go on the
// wire. Before P1 it was suppressed only by a `json:"-"` tag one edit away from
// leaking the tenant key into every response.
func TestWireShape_OrgIDNeverLeaks(t *testing.T) {
	body, err := json.Marshal(ops.ToWire(ops.RcaAgentReport{ID: "r1", OrgID: "secret-org"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"orgId", "OrgID", "org_id", "orgID"} {
		if _, ok := got[k]; ok {
			t.Errorf("wire response carries the tenant key as %q", k)
		}
	}
}

// TestWireShape_FullReport pins the optional fields' presence when set.
func TestWireShape_FullReport(t *testing.T) {
	n := int64(42)
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	body, err := json.Marshal(ops.ToWire(ops.RcaAgentReport{
		ID: "r1", Project: "p", Title: "t", Summary: "s",
		Classification: "mixed", Diagnosis: "d",
		Component:    "svc",
		IssueNumber:  &n,
		IssueURL:     "https://github.com/acme/proj/issues/42",
		IssueTitle:   "it",
		IssueExcerpt: "ex",
		Dispatched:   true,
		Deployed:     true,
		DeployedAt:   &at,
		CreatedAt:    at,
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"component", "issueNumber", "issueUrl", "issueTitle", "issueExcerpt", "deployedAt"} {
		if _, ok := got[k]; !ok {
			t.Errorf("optional field %q is absent when set", k)
		}
	}
	// issueNumber must stay a JSON number, not a string.
	if v, ok := got["issueNumber"].(float64); !ok || v != 42 {
		t.Errorf("issueNumber = %#v, want the number 42", got["issueNumber"])
	}
}
