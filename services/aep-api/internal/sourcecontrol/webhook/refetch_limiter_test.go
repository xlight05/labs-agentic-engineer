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

package webhook

// UNIT tier: the RefetchLimiter token bucket that caps forced webhook-secret
// refetches per (ocOrgID, sourceIP), so a forged-event stream can't amplify
// into git-service load.

import (
	"testing"
	"time"
)

func TestRefetchLimiter_BurstThenDeny(t *testing.T) {
	t.Parallel()
	// A near-zero refill rate isolates the burst: the bucket starts full at
	// `burst`, so exactly `burst` Allow()s pass, then the next is denied.
	const burst = 3
	l := NewRefetchLimiter(0.0001, burst)

	for i := 0; i < burst; i++ {
		if !l.Allow("acme|1.2.3.4") {
			t.Fatalf("token %d within burst must be allowed", i+1)
		}
	}
	if l.Allow("acme|1.2.3.4") {
		t.Fatal("the (burst+1)th refetch must be denied")
	}
}

func TestRefetchLimiter_RefillsOverTime(t *testing.T) {
	t.Parallel()
	// rate=100/s ⇒ one token per 10ms. With the clock frozen, back-to-back calls
	// refill nothing, so the bucket reads empty right after draining; advancing the
	// injected clock 50ms (5 tokens' worth, capped at burst) refills it past one.
	base := time.Unix(1_700_000_000, 0)
	clock := base
	l := NewRefetchLimiter(100, 1)
	l.now = func() time.Time { return clock }

	if !l.Allow("k") {
		t.Fatal("the single burst token must be allowed")
	}
	if l.Allow("k") {
		t.Fatal("bucket must be empty immediately after draining the burst")
	}

	clock = base.Add(50 * time.Millisecond)

	if !l.Allow("k") {
		t.Fatal("bucket must refill past one token after advancing the clock")
	}
}

func TestRefetchLimiter_KeysAreIndependent(t *testing.T) {
	t.Parallel()
	// Buckets are per key: draining one org|ip pair must not throttle another,
	// so one forged sender can't starve legitimate orgs.
	l := NewRefetchLimiter(0.0001, 1)
	if !l.Allow("acme|1.1.1.1") {
		t.Fatal("first key's single token must be allowed")
	}
	if l.Allow("acme|1.1.1.1") {
		t.Fatal("first key must now be empty")
	}
	if !l.Allow("evil|9.9.9.9") {
		t.Fatal("a distinct key must have its own full bucket")
	}
}

func TestNewRefetchLimiter_Defaults(t *testing.T) {
	t.Parallel()
	// Non-positive rate/burst fall back to 1/s, burst 5 — so the burst allows 5.
	l := NewRefetchLimiter(0, 0)
	for i := 0; i < 5; i++ {
		if !l.Allow("k") {
			t.Fatalf("default burst is 5; token %d must be allowed", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("default burst of 5 must deny the 6th")
	}
}
