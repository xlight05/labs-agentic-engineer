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

package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAddCorrelationID_GeneratesWhenAbsent: with no incoming header the
// middleware mints a UUID, echoes it on the response, and puts it in the context
// where GetCorrelationID (and the ContextHandler) read it — the same value in
// all three places.
func TestAddCorrelationID_GeneratesWhenAbsent(t *testing.T) {
	var ctxID string
	h := AddCorrelationID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxID = GetCorrelationID(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	respID := rec.Header().Get(CorrelationIDHeader)
	if respID == "" {
		t.Fatal("no correlation ID echoed on the response")
	}
	if ctxID != respID {
		t.Fatalf("context ID %q != response header ID %q", ctxID, respID)
	}
}

// TestAddCorrelationID_PreservesIncoming: a caller-supplied header is threaded
// through unchanged (distributed-trace continuity).
func TestAddCorrelationID_PreservesIncoming(t *testing.T) {
	const want = "trace-abc-123"
	var got string
	h := AddCorrelationID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetCorrelationID(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(CorrelationIDHeader, want)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got != want {
		t.Fatalf("context ID = %q, want %q", got, want)
	}
	if rec.Header().Get(CorrelationIDHeader) != want {
		t.Fatalf("response header = %q, want %q", rec.Header().Get(CorrelationIDHeader), want)
	}
}

func TestGetCorrelationID_AbsentIsEmpty(t *testing.T) {
	if id := GetCorrelationID(context.Background()); id != "" {
		t.Fatalf("GetCorrelationID(empty ctx) = %q, want \"\"", id)
	}
}

// TestContextHandler_StampsCorrelationID: the global handler enriches every
// context-aware slog record with correlation_id — this is what makes
// slog.InfoContext(ctx, …) carry the ID without a per-request logger.
func TestContextHandler_StampsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))

	ctx := WithCorrelationID(context.Background(), "cid-42")
	logger.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if rec["correlation_id"] != "cid-42" {
		t.Fatalf("correlation_id = %v, want cid-42 (record: %v)", rec["correlation_id"], rec)
	}
}

// TestContextHandler_NoCorrelationIDLeavesRecordClean: without an ID in context
// the handler adds nothing (no empty correlation_id attribute).
func TestContextHandler_NoCorrelationIDLeavesRecordClean(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	logger.InfoContext(context.Background(), "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if _, present := rec["correlation_id"]; present {
		t.Fatalf("correlation_id present with no ID in context: %v", rec)
	}
}
