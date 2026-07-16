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

// Package async is the one spawn helper for detached, fire-and-forget work:
// background watchers and best-effort side effects that must survive the caller
// returning. Go adds the panic barrier every such goroutine needs — an
// unrecovered panic on a bare `go f()` takes down the whole process, and every
// watcher + fire-and-forget task previously relied on that never happening.
package async

import (
	"context"
	"log/slog"
	"runtime/debug"
)

// Go runs fn on a new detached goroutine with a panic barrier: a panic in fn is
// recovered and logged (with the goroutine name + stack) instead of crashing the
// process. name identifies the goroutine in logs. ctx is passed to fn and used
// for the recovery log's context (correlation id); pass a request-independent
// context (context.Background or context.WithoutCancel) for work that must
// outlive the caller, and set any deadline inside fn so its cancel fires when the
// goroutine finishes, not when the caller returns.
//
// Deliberately minimal — no budget/interval/options. Fire-and-forget work that
// needs coordination (a stop signal, a completion handoff) keeps its own channel;
// this is only for the detach + panic-recover + log the bare `go` lacked.
func Go(ctx context.Context, name string, fn func(context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(ctx, "async goroutine panicked (recovered)",
					"goroutine", name, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn(ctx)
	}()
}
