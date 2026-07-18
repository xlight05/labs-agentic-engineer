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

// Package devflow hosts the Temporal-backed development workflows: the
// per-version DevFlowWorkflow (tag → design → plan → task fan-out →
// validate), the per-task TaskFlowWorkflow (dispatch coding agent → PR →
// merge → build → deploy), and the validating phase's tree — the per-version
// ValidationFlowWorkflow orchestrator fanning out ValidationTaskWorkflow lane
// children (dispatch lanes → single PR → merge, no build/deploy).
// Activities are thin adapters over the existing
// feature services; existing webhook handlers and watchers feed the
// workflows via signals (see signaler.go). The whole feature is additive:
// with Temporal unconfigured, nothing here runs and the rest of aep-api is
// untouched.
package devflow

import (
	"errors"
	"log/slog"
	"sync"

	"github.com/wso2/aep/aep-api/internal/config"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
)

// ErrTemporalUnavailable is returned by Runtime.Client while no connection to
// the Temporal server has been established (yet). API handlers map it to
// 503 temporal_unavailable.
var ErrTemporalUnavailable = errors.New("temporal_unavailable")

// Runtime owns the Temporal client connection. The dial happens in the
// worker watcher's retry loop — never at Build time — so aep-api boots and
// serves everything else even when the Temporal server is down.
type Runtime struct {
	cfg config.TemporalConfig

	mu sync.RWMutex
	c  client.Client
}

// NewRuntime creates the runtime; no connection is attempted here.
func NewRuntime(cfg config.TemporalConfig) *Runtime {
	return &Runtime{cfg: cfg}
}

// TaskQueue returns the configured task queue name.
func (r *Runtime) TaskQueue() string { return r.cfg.TaskQueue }

// Available reports whether a Temporal client connection is established.
func (r *Runtime) Available() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.c != nil
}

// Client returns the connected Temporal client, or ErrTemporalUnavailable
// while the worker watcher has not (re)established the connection.
func (r *Runtime) Client() (client.Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.c == nil {
		return nil, ErrTemporalUnavailable
	}
	return r.c, nil
}

// dial establishes the client connection. Called by the worker watcher's
// retry loop; safe to call repeatedly.
func (r *Runtime) dial() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c != nil {
		return nil
	}
	c, err := client.Dial(client.Options{
		HostPort:  r.cfg.HostPort,
		Namespace: r.cfg.Namespace,
		Logger:    slogAdapter{l: slog.Default()},
	})
	if err != nil {
		return err
	}
	r.c = c
	return nil
}

// close tears down the client connection (shutdown path).
func (r *Runtime) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c != nil {
		r.c.Close()
		r.c = nil
	}
}

// slogAdapter bridges Temporal's log.Logger to the process slog default.
type slogAdapter struct{ l *slog.Logger }

var _ log.Logger = slogAdapter{}

func (a slogAdapter) Debug(msg string, keyvals ...interface{}) { a.l.Debug(msg, keyvals...) }
func (a slogAdapter) Info(msg string, keyvals ...interface{})  { a.l.Info(msg, keyvals...) }
func (a slogAdapter) Warn(msg string, keyvals ...interface{})  { a.l.Warn(msg, keyvals...) }
func (a slogAdapter) Error(msg string, keyvals ...interface{}) { a.l.Error(msg, keyvals...) }
