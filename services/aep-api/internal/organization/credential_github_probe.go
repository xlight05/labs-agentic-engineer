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

// credential_github_probe.go — raw GitHub REST probes for the PAT
// path: identity fetch, org-membership validation, repo-read probe.

package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ----------------------------------------------------------------------------
// Validation chain — phase2.md §6.5
// ----------------------------------------------------------------------------

type ghIdentity struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (s *CredentialService) fetchPATIdentity(ctx context.Context, pat string) (*ghIdentity, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.githubAPI+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &ValidationError{Code: "github_unreachable", Message: fmt.Sprintf("GitHub /user unreachable: %v", err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 {
		return nil, &ValidationError{Code: "pat_invalid", Message: "PAT is not a valid GitHub token"}
	}
	if resp.StatusCode == 403 {
		return nil, &ValidationError{Code: "pat_forbidden", Message: "PAT lacks scope or is rate-limited"}
	}
	if resp.StatusCode != 200 {
		return nil, &ValidationError{Code: "github_error", Message: fmt.Sprintf("GET /user: %d %s", resp.StatusCode, truncateForError(body))}
	}
	var id ghIdentity
	if err := json.Unmarshal(body, &id); err != nil {
		return nil, &ValidationError{Code: "github_unmarshal", Message: fmt.Sprintf("decode /user: %v", err)}
	}
	if id.Login == "" {
		return nil, &ValidationError{Code: "github_no_login", Message: "/user response missing login"}
	}
	if id.Name == "" {
		id.Name = id.Login
	}
	if id.Email == "" {
		// User may have private email — fall back to noreply.
		id.Email = fmt.Sprintf("%s@users.noreply.github.com", id.Login)
	}
	return &id, nil
}

func (s *CredentialService) validatePATMembership(ctx context.Context, pat, githubLogin, identityLogin string) error {
	if strings.EqualFold(githubLogin, identityLogin) {
		// PAT owner == githubLogin — no membership probe needed.
		return nil
	}
	url := fmt.Sprintf("%s/user/memberships/orgs/%s", s.githubAPI, githubLogin)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &ValidationError{Code: "github_unreachable", Message: fmt.Sprintf("GitHub membership probe unreachable: %v", err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		return &ValidationError{Code: "pat_not_member", Message: fmt.Sprintf("PAT is not a member of org %q", githubLogin)}
	}
	// 403 from this endpoint typically means the PAT lacks the
	// `read:org` / `Members: read` permission. Fine-grained PATs scoped
	// only to repo operations hit this. The downstream repo-read probe
	// is the real signal — if the PAT can write to the org's repos
	// (which is what AEP actually does), membership is implicit.
	// Skip-with-log so a fine-grained-PAT user isn't blocked from
	// connecting just because they didn't grant membership-read.
	if resp.StatusCode == 403 {
		// Best-effort log; don't fail. The repo-read probe catches
		// the genuine "can't reach this org" failure mode.
		return nil
	}
	if resp.StatusCode != 200 {
		return &ValidationError{Code: "github_error", Message: fmt.Sprintf("membership probe %d: %s", resp.StatusCode, truncateForError(body))}
	}
	var membership struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &membership)
	if !strings.EqualFold(membership.State, "active") {
		return &ValidationError{Code: "pat_membership_inactive", Message: fmt.Sprintf("PAT membership in %q is %q (must be active)", githubLogin, membership.State)}
	}
	return nil
}

func (s *CredentialService) probePATRepoRead(ctx context.Context, pat, githubLogin string) error {
	// List one repo under githubLogin. If empty, accept (fresh org).
	url := fmt.Sprintf("%s/orgs/%s/repos?per_page=1", s.githubAPI, githubLogin)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &ValidationError{Code: "github_unreachable", Message: fmt.Sprintf("GitHub repo probe unreachable: %v", err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		// Could be a user account, not an org — try the user endpoint.
		userURL := fmt.Sprintf("%s/users/%s/repos?per_page=1", s.githubAPI, githubLogin)
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
		req2.Header.Set("Authorization", "Bearer "+pat)
		req2.Header.Set("Accept", "application/vnd.github+json")
		req2.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp2, err2 := s.httpClient.Do(req2)
		if err2 != nil {
			return &ValidationError{Code: "github_unreachable", Message: fmt.Sprintf("GitHub user-repo probe: %v", err2)}
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != 200 {
			b, _ := io.ReadAll(resp2.Body)
			return &ValidationError{Code: "pat_no_repo_read", Message: fmt.Sprintf("PAT scope check failed: cannot read repos under %q (%d %s)", githubLogin, resp2.StatusCode, truncateForError(b))}
		}
		return nil
	}
	if resp.StatusCode == 200 {
		return nil
	}
	if resp.StatusCode == 403 {
		return &ValidationError{Code: "pat_no_repo_read", Message: fmt.Sprintf("PAT scope check failed: cannot read repos under %q", githubLogin)}
	}
	return &ValidationError{Code: "github_error", Message: fmt.Sprintf("repo probe %d: %s", resp.StatusCode, truncateForError(body))}
}
