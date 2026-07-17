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

package sourcecontrol

import (
	"sync"
	"testing"
)

// The bounded-growth guarantee: after every holder releases, the key's entry
// is evicted, so serving many repos over the process lifetime does not leak
// map entries (the reason keyedMutex replaced a naive sync.Map of mutexes).
func TestKeyedMutex_EvictsEntryWhenUnused(t *testing.T) {
	var k keyedMutex

	for i := 0; i < 1000; i++ {
		unlock := k.lock("owner/repo")
		unlock()
	}

	k.mu.Lock()
	n := len(k.locks)
	k.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected no residual lock entries after release, got %d", n)
	}
}

// While a key is held, its entry is retained (a concurrent waiter must find the
// same mutex), and it is evicted only once the last holder/waiter is gone.
func TestKeyedMutex_RetainsWhileHeldEvictsAfter(t *testing.T) {
	var k keyedMutex

	unlock := k.lock("a")
	k.mu.Lock()
	if _, ok := k.locks["a"]; !ok {
		k.mu.Unlock()
		t.Fatal("entry for a held key must be retained")
	}
	k.mu.Unlock()

	unlock()
	k.mu.Lock()
	_, ok := k.locks["a"]
	k.mu.Unlock()
	if ok {
		t.Fatal("entry must be evicted once released")
	}
}

// Two keys serialize independently — locking one never blocks the other.
func TestKeyedMutex_DistinctKeysDoNotBlock(t *testing.T) {
	var k keyedMutex

	unlockA := k.lock("a")
	defer unlockA()

	done := make(chan struct{})
	go func() {
		unlockB := k.lock("b") // must not block on "a"
		unlockB()
		close(done)
	}()

	select {
	case <-done:
	default:
		// Give the goroutine a chance without a timer dependency.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { <-done; wg.Done() }()
		wg.Wait()
	}
}
