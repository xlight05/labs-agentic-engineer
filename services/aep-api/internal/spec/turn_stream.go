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

// turn_stream.go — the in-memory per-turn part broker behind the resumable
// turn stream (design D16/D17): the runner appends every raw StreamPart data
// payload as it taps the agents stream, attachers replay from an index and
// live-tail, and ONE terminal event closes the turn. The buffer is
// deliberately not durable (replica affinity, D17) and is dropped
// turnBufferRetention after the terminal event — an attach after that 404s
// and the FE falls back to the status GET.

import (
	"errors"
	"sync"
	"time"
)

const (
	// turnBufferRetention is how long a finished turn's buffer stays
	// attachable after its terminal event (§17.11 — the documented TTL).
	turnBufferRetention = 120 * time.Second
	// turnBufferMaxParts / turnBufferMaxBytes bound one turn's ring. Turns
	// run minutes and parts are small deltas, so overflow means a runaway
	// stream: the buffer stops STORING (live subscribers keep tailing), new
	// attachers are refused while running (ErrTurnBufferTruncated → 409-ish
	// pre-stream), and a post-terminal attach replays only the terminal
	// event.
	turnBufferMaxParts = 16384
	turnBufferMaxBytes = 16 << 20
	// subscriberChanCap bounds a live subscriber's backlog; a client that
	// cannot drain this many parts is dropped (its channel closes without a
	// terminal event — it reconnects with Last-Event-ID).
	subscriberChanCap = 1024
)

// Broker attach errors, mapped by the HTTP edge.
var (
	// ErrTurnNotBuffered: unknown turn id or buffer expired → 404 pre-stream.
	ErrTurnNotBuffered = errors.New("turn stream is not buffered on this replica")
	// ErrTurnBufferTruncated: the running turn overflowed the ring → the
	// replay would be incomplete; attach again after the terminal.
	ErrTurnBufferTruncated = errors.New("turn stream buffer overflowed — attach after the turn finishes")
)

// BrokerEvent is one buffered stream event: the raw SSE data payload plus its
// absolute index (the SSE `id:`) and whether it is the turn's terminal event.
type BrokerEvent struct {
	Index    int
	Data     []byte
	Terminal bool
}

// TurnSubscription is one attached stream consumer: Replay carries the
// already-buffered events from the requested index; C live-tails until the
// terminal event is delivered (then closes) or the subscriber is dropped for
// falling behind (closed without a terminal event). Cancel is idempotent and
// must be called when the consumer goes away.
type TurnSubscription struct {
	Replay []BrokerEvent
	C      <-chan BrokerEvent
	Cancel func()
}

// TurnBroker fans one running turn's parts out to any number of attachers.
// All methods are safe for concurrent use.
type TurnBroker struct {
	mu        sync.Mutex
	turns     map[string]*turnBuffer
	retention time.Duration
	maxParts  int
	maxBytes  int
}

type turnBuffer struct {
	events    []BrokerEvent // stored events, indices contiguous from 0
	bytes     int
	nextIndex int  // total events ever appended (indices keep counting past overflow)
	truncated bool // ring overflowed: events no longer stored (except terminal)
	done      bool // terminal appended
	subs      map[chan BrokerEvent]struct{}
	expire    *time.Timer
}

// NewTurnBroker builds a broker with the production retention/caps.
func NewTurnBroker() *TurnBroker {
	return &TurnBroker{
		turns:     map[string]*turnBuffer{},
		retention: turnBufferRetention,
		maxParts:  turnBufferMaxParts,
		maxBytes:  turnBufferMaxBytes,
	}
}

// newTurnBrokerForTest overrides retention/caps (tests only).
func newTurnBrokerForTest(retention time.Duration, maxParts, maxBytes int) *TurnBroker {
	return &TurnBroker{turns: map[string]*turnBuffer{}, retention: retention, maxParts: maxParts, maxBytes: maxBytes}
}

// Open registers a turn buffer. Called once per started turn, before the
// first Append. Re-opening an existing id is a no-op.
func (b *TurnBroker) Open(turnID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.turns[turnID]; ok {
		return
	}
	b.turns[turnID] = &turnBuffer{subs: map[chan BrokerEvent]struct{}{}}
}

// Has reports whether the turn is still buffered on this replica.
func (b *TurnBroker) Has(turnID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.turns[turnID]
	return ok
}

// Append buffers one raw data payload and fans it out to live subscribers.
// Appends to an unknown or already-terminal turn are dropped.
func (b *TurnBroker) Append(turnID string, data []byte) {
	b.append(turnID, data, false)
}

// Terminal appends the ONE terminal event, closes every subscriber after
// delivering it, and starts the retention timer. Idempotent — a second
// terminal (e.g. the sweep racing the runner) is dropped.
func (b *TurnBroker) Terminal(turnID string, data []byte) {
	b.append(turnID, data, true)
}

func (b *TurnBroker) append(turnID string, data []byte, terminal bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf, ok := b.turns[turnID]
	if !ok || buf.done {
		return
	}

	ev := BrokerEvent{Index: buf.nextIndex, Data: data, Terminal: terminal}
	buf.nextIndex++

	// Store unless the ring overflowed; the terminal event is always stored
	// (it is small and post-terminal attachers need at least the outcome).
	if terminal || !buf.truncated {
		if !terminal && (len(buf.events) >= b.maxParts || buf.bytes+len(data) > b.maxBytes) {
			buf.truncated = true
		} else {
			buf.events = append(buf.events, ev)
			buf.bytes += len(data)
		}
	}

	// Fan out to live subscribers; a full channel means the consumer fell
	// behind — drop it (close without terminal → the client re-attaches).
	for ch := range buf.subs {
		select {
		case ch <- ev:
		default:
			delete(buf.subs, ch)
			close(ch)
		}
	}

	if terminal {
		buf.done = true
		for ch := range buf.subs {
			delete(buf.subs, ch)
			close(ch)
		}
		buf.expire = time.AfterFunc(b.retention, func() { b.drop(turnID) })
	}
}

// drop removes an expired buffer.
func (b *TurnBroker) drop(turnID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf, ok := b.turns[turnID]
	if !ok {
		return
	}
	for ch := range buf.subs {
		delete(buf.subs, ch)
		close(ch)
	}
	delete(b.turns, turnID)
}

// Subscribe attaches from absolute index `from`: buffered events ≥ from are
// returned as Replay and, for a still-running turn, a live channel tails the
// rest. Post-terminal attaches get a nil-closed channel (everything is in
// Replay). Errors: ErrTurnNotBuffered (unknown/expired), or
// ErrTurnBufferTruncated when a mid-run overflow makes a faithful replay
// impossible (a post-terminal attach on a truncated turn instead replays
// ONLY the terminal event).
func (b *TurnBroker) Subscribe(turnID string, from int) (*TurnSubscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf, ok := b.turns[turnID]
	if !ok {
		return nil, ErrTurnNotBuffered
	}
	if buf.truncated && !buf.done {
		return nil, ErrTurnBufferTruncated
	}
	if from < 0 {
		from = 0
	}

	var replay []BrokerEvent
	if buf.truncated {
		// A truncated buffer cannot replay faithfully (there is a gap between
		// the stored head and the terminal) — serve ONLY the terminal event,
		// which is what a late attacher needs to resolve the turn.
		for _, ev := range buf.events {
			if ev.Terminal {
				replay = append(replay, ev)
			}
		}
	} else {
		for _, ev := range buf.events {
			if ev.Index >= from {
				replay = append(replay, ev)
			}
		}
	}

	closed := make(chan BrokerEvent)
	close(closed)
	sub := &TurnSubscription{Replay: replay, C: closed, Cancel: func() {}}
	if buf.done {
		return sub, nil
	}

	ch := make(chan BrokerEvent, subscriberChanCap)
	buf.subs[ch] = struct{}{}
	sub.C = ch
	var once sync.Once
	sub.Cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if cur, ok := b.turns[turnID]; ok {
				if _, live := cur.subs[ch]; live {
					delete(cur.subs, ch)
					close(ch)
				}
			}
		})
	}
	return sub, nil
}
