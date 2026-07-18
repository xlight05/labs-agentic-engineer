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

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
)

// Router dispatches a verified, dedup'd delivery to the handlers registered for
// an event class. Multiple handlers may register for the same (event, action) —
// they run in registration order and their errors aggregate (so several features
// can independently observe the same GitHub event, e.g. issues/closed).
// Events with no registered handler are logged and no-op'd.
type Router struct {
	handlers map[string][]EventHandler
}

// EventHandler is the contract every per-event handler implements. The
// (event, action) tuple drives dispatch; the raw payload is provided for
// the handler to parse what it needs. Idempotency is the handler's
// responsibility — a redelivery may invoke the handler again.
type EventHandler interface {
	Handle(ctx context.Context, event, action string, payload []byte) error
}

// EventHandlerFunc adapts a function to EventHandler.
type EventHandlerFunc func(ctx context.Context, event, action string, payload []byte) error

func (f EventHandlerFunc) Handle(ctx context.Context, event, action string, payload []byte) error {
	return f(ctx, event, action, payload)
}

func NewRouter() *Router {
	return &Router{handlers: map[string][]EventHandler{}}
}

// Register appends a handler for an event class. Pass action="" to register a
// fallback for the event when no action-specific handler matches. Handlers for
// the same key run in registration order.
//
// Lookup order: (event, action) → (event, "") → log + no-op.
func (r *Router) Register(event, action string, h EventHandler) {
	k := key(event, action)
	r.handlers[k] = append(r.handlers[k], h)
}

// Dispatch parses the action from the payload and runs every handler registered
// for the matching key (action-specific handlers win over the event fallback;
// the fallback runs only when no action-specific handler exists). All matching
// handlers run even if one errors; the joined error is returned — the receiver
// decides ack 200 vs. 5xx based on whether it is nil.
func (r *Router) Dispatch(ctx context.Context, event string, payload []byte) error {
	action := parseAction(payload)
	if hs, ok := r.handlers[key(event, action)]; ok {
		return runAll(ctx, hs, event, action, payload)
	}
	if hs, ok := r.handlers[key(event, "")]; ok {
		return runAll(ctx, hs, event, action, payload)
	}
	// Persisted, no-op — unknown events are intentionally swallowed.
	slog.DebugContext(ctx, "webhook: no handler", "event", event, "action", action, "result", "unhandled_event")
	return nil
}

// runAll invokes every handler, collecting errors so one handler's failure does
// not prevent the others (independent subscribers). errors.Join returns nil for
// an all-success run and preserves errors.Is/As on the aggregate.
func runAll(ctx context.Context, hs []EventHandler, event, action string, payload []byte) error {
	var errs []error
	for _, h := range hs {
		if err := h.Handle(ctx, event, action, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ParseAction returns the payload's "action" field if present, else "".
func parseAction(payload []byte) string {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return ""
	}
	return body.Action
}

func key(event, action string) string {
	if action == "" {
		return event
	}
	return event + ":" + action
}
