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

package delivery_test

import (
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

func TestTaskStreamHub_NotifyWakesMatchingSubscriber(t *testing.T) {
	h := delivery.NewTaskStreamHub()
	ch, cancel := h.Subscribe("acme/widgets", 7)
	defer cancel()

	h.Notify("acme/widgets", 7)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("subscriber not woken by a matching Notify")
	}
}

func TestTaskStreamHub_NotifyIgnoresOtherKeys(t *testing.T) {
	h := delivery.NewTaskStreamHub()
	ch, cancel := h.Subscribe("acme/widgets", 7)
	defer cancel()

	h.Notify("acme/widgets", 8) // different issue
	h.Notify("acme/gadgets", 7) // different repo
	select {
	case <-ch:
		t.Fatal("woken by a Notify for a different (repo, issue)")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTaskStreamHub_CoalescesRepeatedNotify(t *testing.T) {
	h := delivery.NewTaskStreamHub()
	ch, cancel := h.Subscribe("acme/widgets", 7)
	defer cancel()

	h.Notify("acme/widgets", 7)
	h.Notify("acme/widgets", 7)
	h.Notify("acme/widgets", 7)

	<-ch // one wake-up
	select {
	case <-ch:
		t.Fatal("repeated notifies must coalesce to a single wake-up")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTaskStreamHub_CancelDeregisters(t *testing.T) {
	h := delivery.NewTaskStreamHub()
	ch, cancel := h.Subscribe("acme/widgets", 7)
	cancel()
	cancel() // idempotent — must not panic

	h.Notify("acme/widgets", 7)
	select {
	case <-ch:
		t.Fatal("a cancelled subscriber must not receive")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTaskStreamHub_NilIsNoop(t *testing.T) {
	var h *delivery.TaskStreamHub
	h.Notify("acme/widgets", 7) // nil-safe: the writers hold it unconditionally
}
