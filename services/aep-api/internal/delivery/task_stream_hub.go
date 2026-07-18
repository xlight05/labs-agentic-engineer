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

import (
	"strconv"
	"sync"
)

// TaskStreamHub is the in-process change bus behind the task-log SSE stream: a
// notify-only broker keyed by (repo, issue). It carries NO payload and buffers
// NO history — it only wakes attached stream connections to re-derive the
// Task's state from the durable sources. This is the deliberate no-schema
// design (docs/design/task-log-stream.md §Backend): the writers that already
// know a Task changed (the PR webhook, the job/exec watchers, the funnel) ping
// the hub; a slow re-derive tick on each connection is the safety net so a
// missed ping degrades to latency, not wrongness.
//
// Single-replica assumption (aep-api runs one replica today): the bus is
// in-memory. The known evolution when the BFF scales out is LISTEN/NOTIFY (or a
// durable task_events table) — the connection-side re-derive contract does not
// change.
type TaskStreamHub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewTaskStreamHub builds an empty hub.
func NewTaskStreamHub() *TaskStreamHub {
	return &TaskStreamHub{subs: map[string]map[chan struct{}]struct{}{}}
}

func hubKey(repo string, issue int) string {
	return repo + "#" + strconv.Itoa(issue)
}

// Subscribe registers a listener for (repo, issue). The returned channel is
// signalled (coalesced) on every Notify; cancel deregisters it and is
// idempotent. The channel is buffered(1) so a notify never blocks the writer
// and repeated notifies between drains collapse into one wake-up.
func (h *TaskStreamHub) Subscribe(repo string, issue int) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	key := hubKey(repo, issue)

	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[chan struct{}]struct{}{}
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if m := h.subs[key]; m != nil {
				delete(m, ch)
				if len(m) == 0 {
					delete(h.subs, key)
				}
			}
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

// Notify wakes every listener attached to (repo, issue). Nil-safe — the writer
// features hold the hub unconditionally (exactly the devflow-signaler pattern),
// so a hub that was never wired is a silent no-op. Non-blocking: a listener
// whose buffer is already full is left alone (it will re-derive on the next
// drain anyway).
func (h *TaskStreamHub) Notify(repo string, issue int) {
	if h == nil {
		return
	}
	key := hubKey(repo, issue)
	h.mu.Lock()
	chans := make([]chan struct{}, 0, len(h.subs[key]))
	for ch := range h.subs[key] {
		chans = append(chans, ch)
	}
	h.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
