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

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/platform/auth"
)

// Client fetches build logs from the observability service.
type Client interface {
	// since narrows the query window to entries after that instant — the tail
	// read behind the console's log cursor. A zero `since` reads the whole
	// retention window.
	GetBuildLogs(ctx context.Context, orgName, projectName, componentName, buildName string, since time.Time) (*gen.BuildLogs, error)
}

type observabilityClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new observability client.
// baseURL is the observability service base URL (e.g. http://host:port).
func NewClient(baseURL string) Client {
	return &observabilityClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type buildLogsRequest struct {
	ComponentName string    `json:"componentName"`
	NamespaceName string    `json:"namespaceName"`
	ProjectName   string    `json:"projectName"`
	StartTime     time.Time `json:"startTime"`
	EndTime       time.Time `json:"endTime"`
	Limit         int       `json:"limit,omitempty"`
	SortOrder     string    `json:"sortOrder,omitempty"`
}

type logEntry struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Log       *string    `json:"log,omitempty"`
	Level     *string    `json:"level,omitempty"`
}

type buildLogsResponse struct {
	Logs       *[]logEntry `json:"logs,omitempty"`
	TotalCount *int        `json:"totalCount,omitempty"`
}

func (c *observabilityClient) GetBuildLogs(ctx context.Context, orgName, projectName, componentName, buildName string, since time.Time) (*gen.BuildLogs, error) {
	now := time.Now()
	// A cursor read starts at the cursor; a first read takes the whole retention
	// window. Narrowing here rather than only filtering the response keeps a
	// tail poll cheap on the observability service too.
	start := now.Add(-30 * 24 * time.Hour)
	if !since.IsZero() && since.After(start) {
		start = since
	}
	body := buildLogsRequest{
		ComponentName: componentName,
		NamespaceName: orgName,
		ProjectName:   projectName,
		StartTime:     start,
		EndTime:       now,
		Limit:         1000,
		SortOrder:     "asc",
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("observability: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/logs/build/%s", c.baseURL, buildName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("observability: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := auth.GetAuthToken(ctx); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("observability: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("observability: unexpected status %d", resp.StatusCode)
	}

	var result buildLogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("observability: decode response: %w", err)
	}

	logs := &gen.BuildLogs{}
	if result.TotalCount != nil {
		logs.TotalCount = int64(*result.TotalCount)
	}
	if result.Logs != nil {
		for _, e := range *result.Logs {
			entry := gen.BuildLogEntry{}
			if e.Timestamp != nil {
				entry.Timestamp = e.Timestamp.UTC().Format(time.RFC3339)
			}
			if e.Log != nil {
				entry.Log = *e.Log
			}
			if e.Level != nil {
				entry.Level = *e.Level
			}
			logs.Logs = append(logs.Logs, entry)
		}
	}
	if logs.Logs == nil {
		logs.Logs = []gen.BuildLogEntry{}
	}
	return logs, nil
}
