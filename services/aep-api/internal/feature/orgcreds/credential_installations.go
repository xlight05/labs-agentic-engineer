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

// credential_installations.go — App-installation lifecycle: webhook
// routing lookups (installation id / repo full name), suspend/unsuspend,
// selected-repo merge, installation + repo fetches, and the OAuth
// discover-then-bind path.

package orgcreds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/models"
)

// ----------------------------------------------------------------------------
// Routing lookup — used by the BFF webhook receiver
// ----------------------------------------------------------------------------

// OrgIDByInstallationID returns the ocOrgId bound to the given
// installation_id. Used by the BFF webhook receiver to route App-mode
// events. NotFoundError if no row matches.
func (s *CredentialService) OrgIDByInstallationID(ctx context.Context, installationID int64) (string, error) {
	var row models.OrgCredential
	err := s.db.WithContext(ctx).Where("installation_id = ?", installationID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", &NotFoundError{What: fmt.Sprintf("installation %d", installationID)}
	}
	if err != nil {
		return "", err
	}
	return row.OcOrgID, nil
}

// OrgIDByRepoFullName returns the ocOrgId that owns the given GitHub repo
// (full_name = "owner/repo"). Resolved against git_repositories.org_id —
// every provisioned repo carries the OC org slug it belongs to.
//
// Used by the BFF webhook receiver to route PAT-mode (and App-mode
// per-repo) events: pull_request, push, issue_comment, issues. The
// installation-id-based path handles the App-mode lifecycle events;
// repo-keyed events use this lookup so PAT-mode events route correctly.
func (s *CredentialService) OrgIDByRepoFullName(ctx context.Context, fullName string) (string, error) {
	if fullName == "" {
		return "", &NotFoundError{What: "empty repo full_name"}
	}
	// INT-2 (routing leg): match the canonical clone URL EXACTLY, not with an
	// unanchored `LIKE '%/owner/repo'`. The leading-`%` wildcard matched any
	// host (and any path suffix), so a malicious or colliding repo_url could
	// route a webhook to the wrong org. git_repositories stores the canonical
	// `https://github.com/<owner>/<repo>` (optionally `.git`); match both exact
	// shapes, anchored on host+owner+repo. No `LIKE`, no leading wildcard.
	var row struct {
		OrgID string `gorm:"column:org_id"`
	}
	canonical := "https://github.com/" + fullName
	err := s.db.WithContext(ctx).
		Table("git_repositories").
		Select("org_id").
		Where("repo_url = ? OR repo_url = ?", canonical, canonical+".git").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return "", fmt.Errorf("repo lookup: %w", err)
	}
	if row.OrgID == "" {
		return "", &NotFoundError{What: fmt.Sprintf("repo %s", fullName)}
	}
	return row.OrgID, nil
}

// SuspendInstallation flips the org_credentials row bound to installationID
// to status='suspended'. Idempotent.
func (s *CredentialService) SuspendInstallation(ctx context.Context, installationID int64) error {
	return s.setInstallationStatus(ctx, installationID, "suspended")
}

// UnsuspendInstallation flips the row to status='active'. Idempotent.
func (s *CredentialService) UnsuspendInstallation(ctx context.Context, installationID int64) error {
	return s.setInstallationStatus(ctx, installationID, "active")
}

func (s *CredentialService) setInstallationStatus(ctx context.Context, installationID int64, status string) error {
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback() //nolint:errcheck
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, fmt.Sprintf("install:%d", installationID)).Error; err != nil {
		return err
	}
	res := tx.Model(&models.OrgCredential{}).
		Where("installation_id = ?", installationID).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 200 idempotent — webhooks may arrive before the connect callback
		// has finished; missing row is recoverable via the next connect.
		_ = tx.Commit()
		return nil
	}
	return tx.Commit().Error
}

// MergeSelectedRepos applies an installation_repositories.added/removed
// JSON merge under the org-scoped lock. delta carries lists of full names
// to add/remove (intersection vs. current state determines the new set).
func (s *CredentialService) MergeSelectedRepos(ctx context.Context, installationID int64, added, removed []string) error {
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback() //nolint:errcheck

	var row models.OrgCredential
	if err := tx.Where("installation_id = ?", installationID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &NotFoundError{What: fmt.Sprintf("installation %d", installationID)}
		}
		return err
	}
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, "org:"+row.OcOrgID).Error; err != nil {
		return err
	}

	current := map[string]bool{}
	for _, r := range row.SelectedRepos {
		current[r] = true
	}
	for _, r := range removed {
		delete(current, r)
	}
	for _, r := range added {
		current[r] = true
	}
	merged := make([]string, 0, len(current))
	for r := range current {
		merged = append(merged, r)
	}

	now := time.Now().UTC()
	if err := tx.Model(&models.OrgCredential{}).
		Where("oc_org_id = ?", row.OcOrgID).
		Updates(map[string]any{
			"selected_repos":    models.JSONStringList(merged),
			"last_validated_at": now,
		}).Error; err != nil {
		return err
	}
	return tx.Commit().Error
}

// fetchInstallation mints an App JWT and reads /app/installations/{id} to
// extract account.{login,type} + selected_repos. Used at App-mode connect.
// accountType is "Organization" or "User" — the connect path refuses
// "User" because GitHub's POST /user/repos endpoint is not accessible to
// App installation tokens, so repo provisioning would 403 silently.
func (s *CredentialService) fetchInstallation(ctx context.Context, installationID int64) (accountLogin, accountType string, selectedRepos []string, err error) {
	jwt, err := s.minter.SignAppJWT(time.Now())
	if err != nil {
		return "", "", nil, &ConflictError{Reason: fmt.Sprintf("app sign: %v", err)}
	}
	url := fmt.Sprintf("%s/app/installations/%d", s.githubAPI, installationID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch install: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		return "", "", nil, &ValidationError{Code: "installation_not_found", Message: fmt.Sprintf("installation %d not found (was the App uninstalled?)", installationID)}
	}
	if resp.StatusCode != 200 {
		return "", "", nil, &ValidationError{Code: "github_error", Message: fmt.Sprintf("GET /app/installations/%d: %d %s", installationID, resp.StatusCode, truncateForError(body))}
	}
	var inst struct {
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		RepositorySelection string `json:"repository_selection"`
	}
	if err := json.Unmarshal(body, &inst); err != nil {
		return "", "", nil, fmt.Errorf("decode install: %w", err)
	}
	if inst.Account.Login == "" {
		return "", "", nil, &ValidationError{Code: "install_no_account", Message: "installation response missing account.login"}
	}

	// Pull the selected-repos list. Empty for repository_selection=all.
	selectedRepos, err = s.listInstallationRepos(ctx, installationID)
	if err != nil {
		// Best-effort — log and continue with empty list.
		slog.WarnContext(ctx, "list installation repos failed", "installationId", installationID, "error", err)
	}

	return inst.Account.Login, inst.Account.Type, selectedRepos, nil
}

// ListInstallationRepos calls GET /installation/repositories with a fresh
// installation token. Used by the reach-reconciliation Phase B cascade to
// confirm GitHub agrees the install has shrunk before abandoning tasks
// (§6.8). Public wrapper around the private helper that's also used by the
// connect flow.
func (s *CredentialService) ListInstallationRepos(ctx context.Context, installationID int64) ([]string, error) {
	return s.listInstallationRepos(ctx, installationID)
}

// ----------------------------------------------------------------------------
// Connect resolution — user-scoped install discovery
// ----------------------------------------------------------------------------

// ErrAppBindNotConfigured is returned when the App or OAuth client secret
// is not configured — the operator hasn't completed the App setup.
var ErrAppBindNotConfigured = errors.New("app bind path not configured (missing app key or oauth client secret)")

// ResolveUserInstallations exchanges an OAuth code for a user-token,
// fetches the installations the user has admin access to via
// GET /user/installations, and intersects with our App's installations.
// Only installations that are either unbound or bound to the requesting
// org are returned — installs bound to *other* AEP orgs are silently
// filtered to avoid leaking cross-tenant install metadata to OC admins
// who happen to share GitHub admin access.
//
// The user-token is used only inside this call and discarded — it never
// crosses any process boundary, never lands in storage, never logged.
//
// There is no "list every install of our App" surface — discovery is
// always proven to the requesting user via OAuth.
func (s *CredentialService) ResolveUserInstallations(ctx context.Context, ocOrgID, oauthCode, redirectURI string) ([]gitrepo.AppInstallationSummary, error) {
	if s.minter == nil || s.minter.AppID() == 0 || s.githubClient == nil {
		return nil, ErrAppBindNotConfigured
	}
	if s.appClientID == "" || s.appClientSecret == "" {
		return nil, ErrAppBindNotConfigured
	}
	if ocOrgID == "" {
		return nil, &ValidationError{Code: "oc_org_id_missing", Message: "ocOrgID is required"}
	}
	if oauthCode == "" {
		return nil, &ValidationError{Code: "oauth_code_missing", Message: "oauthCode is required"}
	}

	userToken, err := s.githubClient.ExchangeOAuthCode(ctx, s.appClientID, s.appClientSecret, oauthCode, redirectURI)
	if err != nil {
		return nil, &ValidationError{Code: "oauth_exchange_failed", Message: err.Error()}
	}
	if userToken == "" {
		return []gitrepo.AppInstallationSummary{}, nil
	}

	userInstalls, err := s.githubClient.GetUserInstallations(ctx, userToken)
	if err != nil {
		return nil, fmt.Errorf("get user installations: %w", err)
	}
	userInstallSet := make(map[int64]struct{}, len(userInstalls))
	for _, id := range userInstalls {
		userInstallSet[id] = struct{}{}
	}

	all, err := s.githubClient.ListAppInstallations(ctx, s.minter)
	if err != nil {
		return nil, fmt.Errorf("list app installations: %w", err)
	}

	// Pull installations bound to OTHER orgs — we filter those out so we
	// don't leak "install X is owned by some other AEP tenant" to this
	// user. Installs bound to ocOrgID itself (re-connect / re-confirm)
	// are kept.
	type boundRow struct {
		InstallationID int64
		OcOrgID        string
	}
	var bound []boundRow
	if err := s.db.WithContext(ctx).
		Model(&models.OrgCredential{}).
		Where("installation_id IS NOT NULL AND status IN ?", []string{"active", "suspended"}).
		Select("installation_id, oc_org_id").
		Find(&bound).Error; err != nil {
		return nil, fmt.Errorf("scan bound installs: %w", err)
	}
	boundElsewhere := make(map[int64]struct{}, len(bound))
	for _, b := range bound {
		if b.OcOrgID != ocOrgID {
			boundElsewhere[b.InstallationID] = struct{}{}
		}
	}

	candidates := make([]gitrepo.AppInstallationSummary, 0, len(all))
	for _, inst := range all {
		if _, ok := userInstallSet[inst.InstallationID]; !ok {
			continue
		}
		if _, ok := boundElsewhere[inst.InstallationID]; ok {
			continue
		}
		candidates = append(candidates, inst)
	}
	return candidates, nil
}

func (s *CredentialService) listInstallationRepos(ctx context.Context, installationID int64) ([]string, error) {
	token, _, err := s.minter.MintForInstallation(ctx, installationID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	page := 1
	for {
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", s.githubAPI, page)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return out, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("list repos: %d %s", resp.StatusCode, truncateForError(body))
		}
		var page1 struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(body, &page1); err != nil {
			return out, err
		}
		for _, r := range page1.Repositories {
			out = append(out, r.FullName)
		}
		if len(page1.Repositories) < 100 {
			break
		}
		page++
	}
	return out, nil
}

// fetchAppBotIdentity calls GET /app to learn the App's bot login. The
// "name" field is the App's display name; the "slug" is what appears as
// `<slug>[bot]` on commits.
func (s *CredentialService) fetchAppBotIdentity(ctx context.Context) (secrets.Identity, error) {
	jwt, err := s.minter.SignAppJWT(time.Now())
	if err != nil {
		return secrets.Identity{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.githubAPI+"/app", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return secrets.Identity{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return secrets.Identity{}, fmt.Errorf("GET /app: %d %s", resp.StatusCode, truncateForError(body))
	}
	var info struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return secrets.Identity{}, err
	}
	if info.Slug == "" {
		return secrets.Identity{}, errors.New("/app response missing slug")
	}
	// GitHub's commit-attribution convention uses {numericUserID}+{slug}[bot]
	// as the noreply email's local-part. We don't have the numeric user ID
	// at this layer; leave the slug-based shape (GitHub still attributes
	// correctly when the email belongs to the App's verified noreply domain).
	return secrets.Identity{
		Name:  info.Name,
		Email: fmt.Sprintf("%s[bot]@users.noreply.github.com", info.Slug),
		Login: fmt.Sprintf("%s[bot]", info.Slug),
	}, nil
}
