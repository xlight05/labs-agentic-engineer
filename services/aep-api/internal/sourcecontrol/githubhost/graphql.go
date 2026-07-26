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

package githubhost

// The GraphQL transport. GitHub's v4 API is a single POST endpoint, so this is
// one function; the operations that use it live with their REST siblings
// (milestones.go). It exists only where GraphQL answers a question REST cannot
// answer in one call — today, the milestone dispatch predicate, which needs
// label-filtered OPEN-issue counts that REST would charge a full issue listing
// for and that the milestone's own open_issues field gets wrong (it counts PRs).
//
// Auth goes through the same authHeaders path as every REST call, so the
// package-level "only place that builds Authorization: Bearer headers" claim
// still holds and short-lived App tokens refresh identically.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// defaultGraphQLEndpoint is GitHub's v4 endpoint. Unlike the REST base it is
// not derivable from apiBase (github.com/graphql is a separate host path), so
// it is its own field with its own option.
const defaultGraphQLEndpoint = "https://api.github.com/graphql"

// WithGraphQLEndpoint overrides the GraphQL endpoint URL. This is a TEST SEAM —
// the sibling of WithAPIBase, letting the gittest tier point the real client at
// an httptest fake's /graphql route. Not wired in production.
//
//deadcode:keep test seam — points the real client at an httptest server; used by internal/sourcecontrol/*_test.go (cross-package).
func WithGraphQLEndpoint(url string) Option {
	return func(c *Client) { c.graphqlEndpoint = url }
}

// graphQL executes one GraphQL operation and decodes the response's `data` into
// out (nil out discards it).
//
// A GraphQL response is 200 with an envelope, so two failure modes exist and
// stay distinguishable: a non-200 is *sourcecontrol.HTTPStatusError (transport
// / auth), a populated errors[] is *sourcecontrol.GraphQLError carrying every
// entry — callers branch on the machine-readable type rather than parsing a
// message. Partial results (data alongside errors) are treated as failures:
// every operation here reads a value it cannot default.
func (c *Client) graphQL(ctx context.Context, cred secrets.Credential, query string, variables map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if err := authHeaders(ctx, req, cred); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github graphql request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &sourcecontrol.HTTPStatusError{
			StatusCode: resp.StatusCode, Body: string(respBody), URL: c.graphqlEndpoint,
		}
	}

	var envelope struct {
		Errors []sourcecontrol.GraphQLErrorDetail `json:"errors"`
		Data   json.RawMessage                    `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return &sourcecontrol.GraphQLError{Errors: envelope.Errors, Query: query}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("github graphql response carried neither data nor errors")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return nil
}
