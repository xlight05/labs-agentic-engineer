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

package spec

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func ev(i int) []byte { return []byte(fmt.Sprintf(`{"n":%d}`, i)) }

func TestBroker_ReplayFromIndex(t *testing.T) {
	b := NewTurnBroker()
	b.Open("t1")
	for i := 0; i < 5; i++ {
		b.Append("t1", ev(i))
	}
	sub, err := b.Subscribe("t1", 3)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Cancel()
	if len(sub.Replay) != 2 || sub.Replay[0].Index != 3 || string(sub.Replay[1].Data) != `{"n":4}` {
		t.Fatalf("replay = %+v", sub.Replay)
	}
	// Negative from clamps to 0 (full replay).
	all, err := b.Subscribe("t1", -7)
	if err != nil || len(all.Replay) != 5 {
		t.Fatalf("full replay = %+v (%v)", all.Replay, err)
	}
	all.Cancel()
}

func TestBroker_LiveTailAndConcurrentViewers(t *testing.T) {
	b := NewTurnBroker()
	b.Open("t1")
	b.Append("t1", ev(0))

	first, err := b.Subscribe("t1", 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	defer first.Cancel()
	second, err := b.Subscribe("t1", 1) // mid-stream viewer, skips the replayed head
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	defer second.Cancel()

	b.Append("t1", ev(1))
	b.Terminal("t1", []byte(`{"type":"turn-committed"}`))

	drain := func(name string, sub *TurnSubscription, wantLive int) []BrokerEvent {
		t.Helper()
		var got []BrokerEvent
		got = append(got, sub.Replay...)
		timeout := time.After(2 * time.Second)
		for {
			select {
			case e, ok := <-sub.C:
				if !ok {
					return got
				}
				got = append(got, e)
			case <-timeout:
				t.Fatalf("%s: timed out draining", name)
			}
		}
	}
	got1 := drain("first", first, 2)
	if len(got1) != 3 || !got1[2].Terminal || got1[2].Index != 2 {
		t.Fatalf("first viewer events = %+v", got1)
	}
	got2 := drain("second", second, 2)
	if len(got2) != 2 || got2[0].Index != 1 || !got2[1].Terminal {
		t.Fatalf("second viewer events = %+v", got2)
	}
}

func TestBroker_PostTerminalAttachAndIdempotentTerminal(t *testing.T) {
	b := NewTurnBroker()
	b.Open("t1")
	b.Append("t1", ev(0))
	b.Terminal("t1", []byte(`{"type":"turn-failed"}`))
	b.Terminal("t1", []byte(`{"type":"SECOND"}`)) // dropped
	b.Append("t1", ev(9))                         // post-terminal append dropped

	sub, err := b.Subscribe("t1", 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(sub.Replay) != 2 || !sub.Replay[1].Terminal || string(sub.Replay[1].Data) != `{"type":"turn-failed"}` {
		t.Fatalf("replay = %+v", sub.Replay)
	}
	if _, ok := <-sub.C; ok {
		t.Fatal("post-terminal channel must be closed")
	}
}

func TestBroker_ExpiryDropsBuffer(t *testing.T) {
	b := newTurnBrokerForTest(20*time.Millisecond, 16, 1<<20)
	b.Open("t1")
	b.Terminal("t1", []byte(`{"type":"turn-committed"}`))

	deadline := time.Now().Add(2 * time.Second)
	for b.Has("t1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.Has("t1") {
		t.Fatal("buffer never expired")
	}
	if _, err := b.Subscribe("t1", 0); !errors.Is(err, ErrTurnNotBuffered) {
		t.Fatalf("post-expiry Subscribe err = %v, want ErrTurnNotBuffered", err)
	}
	// Unknown turn behaves the same.
	if _, err := b.Subscribe("nope", 0); !errors.Is(err, ErrTurnNotBuffered) {
		t.Fatalf("unknown Subscribe err = %v", err)
	}
}

func TestBroker_OverflowTruncates(t *testing.T) {
	b := newTurnBrokerForTest(time.Minute, 3, 1<<20)
	b.Open("t1")
	live, err := b.Subscribe("t1", 0)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	defer live.Cancel()

	for i := 0; i < 5; i++ { // 3 stored, 2 overflow
		b.Append("t1", ev(i))
	}
	// New attacher while running + truncated → refused.
	if _, err := b.Subscribe("t1", 0); !errors.Is(err, ErrTurnBufferTruncated) {
		t.Fatalf("truncated running Subscribe err = %v, want ErrTurnBufferTruncated", err)
	}

	b.Terminal("t1", []byte(`{"type":"turn-committed"}`))

	// The live subscriber saw every event (fan-out is not truncated) + terminal.
	var liveGot []BrokerEvent
	for e := range live.C {
		liveGot = append(liveGot, e)
	}
	if len(liveGot) != 6 || !liveGot[5].Terminal || liveGot[5].Index != 5 {
		t.Fatalf("live events = %+v", liveGot)
	}

	// Post-terminal attach on a truncated turn replays ONLY the terminal (a
	// gapped part replay would be a lie; the terminal resolves the turn).
	sub, err := b.Subscribe("t1", 0)
	if err != nil {
		t.Fatalf("post-terminal Subscribe: %v", err)
	}
	if len(sub.Replay) != 1 || !sub.Replay[0].Terminal || sub.Replay[0].Index != 5 {
		t.Fatalf("truncated replay = %+v", sub.Replay)
	}
}

func TestBroker_SlowSubscriberDroppedWithoutTerminal(t *testing.T) {
	b := newTurnBrokerForTest(time.Minute, 1<<20, 1<<20)
	b.Open("t1")
	sub, err := b.Subscribe("t1", 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Cancel()
	// Never drain: overflow the subscriber channel → dropped (closed without
	// a terminal event having been delivered).
	for i := 0; i < subscriberChanCap+8; i++ {
		b.Append("t1", ev(i))
	}
	sawTerminal := false
	for e := range sub.C {
		if e.Terminal {
			sawTerminal = true
		}
	}
	if sawTerminal {
		t.Fatal("dropped subscriber must not see a terminal event")
	}
}
