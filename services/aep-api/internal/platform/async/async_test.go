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

package async

import (
	"context"
	"testing"
	"time"
)

// TestGo_RunsFnWithContext: fn runs on a goroutine and receives the context.
func TestGo_RunsFnWithContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	got := make(chan any, 1)
	Go(ctx, "runs", func(c context.Context) { got <- c.Value(ctxKey{}) })

	select {
	case v := <-got:
		if v != "v" {
			t.Fatalf("fn saw ctx value %v, want \"v\"", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fn did not run within 2s")
	}
}

// TestGo_RecoversPanic: a panicking fn is recovered — the goroutine unwinds
// through the barrier without crashing the process (this test process staying
// alive to assert afterwards IS the proof).
func TestGo_RecoversPanic(t *testing.T) {
	done := make(chan struct{})
	Go(context.Background(), "panics", func(context.Context) {
		defer close(done) // runs during the panic unwind, before the barrier recovers
		panic("boom")
	})

	select {
	case <-done:
		// The panic was contained by Go's recover; if it had escaped, the test
		// binary would have crashed instead of reaching here.
	case <-time.After(2 * time.Second):
		t.Fatal("panicking fn did not run within 2s")
	}
	// Give the deferred recover a moment to run, then confirm we're still alive.
	time.Sleep(10 * time.Millisecond)
}
