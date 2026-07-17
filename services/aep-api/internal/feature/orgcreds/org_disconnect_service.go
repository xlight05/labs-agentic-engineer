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

package orgcreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// ErrOrgNotFound surfaces from OrgDisconnectService when no credential
// row matches the requested ocOrgId.
var ErrOrgNotFound = errors.New("org credentials: not found")

// OrgDisconnectService runs the BFF-side disconnect cascade defined in
// phase2.md §6.7.
//
// Phase A (confirm — sub-second; runs synchronously on the request):
//   - Calls git-service's internal projection to confirm the row exists.
//     There is no intermediate 'disconnecting' status: the credential row's
//     status CHECK constraint only permits active/suspended/disconnected, and
//     because Phases A–D run synchronously on this path the finalize goes
//     straight to 'disconnected' in Phase D (phase2.md §6.7's staged
//     intermediate state was never wired).
//
// Phase D (org-scoped finalize — git-service GC):
//   - DELETE /internal/credentials/orgs/{ocOrgId} on git-service. Git-service
//     marks status='disconnected' and best-effort GCs OpenBao keys.
//
// Under the tasks-github-native model Tasks are GitHub issues (no
// component_tasks rows to abandon): the old Phase B/C task cascade is gone.
// Severing the credential makes the org's issues inert to the webhook router
// (no valid delivery), which is the disconnect effect.
type OrgDisconnectService struct {
	db       *gorm.DB
	credSvc  *CredentialService
	issueSvc sourcecontrol.IssueService
	// workspaceTrash, when set (from the composition root), is Phase F:
	// rename the org's whole repos/<orgId>/ workspace subtree (all projects
	// incl. _skills) into trash (design §14/D12). Best-effort by contract —
	// it returns nothing and never fails the cascade.
	workspaceTrash func(ctx context.Context, ocOrgID string)
}

// NewOrgDisconnectService constructs the cascade orchestrator.
func NewOrgDisconnectService(
	db *gorm.DB,
	credSvc *CredentialService,
	issueSvc sourcecontrol.IssueService,
) *OrgDisconnectService {
	return &OrgDisconnectService{
		db:       db,
		credSvc:  credSvc,
		issueSvc: issueSvc,
	}
}

// WithWorkspaceTrash installs the Phase-F disk-trash hook (nil-safe).
// Returns s for chaining at the construction sites.
func (s *OrgDisconnectService) WithWorkspaceTrash(fn func(ctx context.Context, ocOrgID string)) *OrgDisconnectService {
	s.workspaceTrash = fn
	return s
}

// Disconnect runs the cascade synchronously. `cause` is recorded on each
// cascaded task's Cause column so audit can distinguish manual disconnect
// from validator/webhook-driven cascades. Empty cause defaults to
// "org.disconnected".
//
// uninstallApp triggers Phase E (GitHub-side App uninstall via
// DELETE /app/installations/{id}) for App-mode connections. Set true for
// manual disconnects so the install on github.com is removed alongside
// the platform row — no orphans left behind. PAT-mode rows ignore the
// flag; webhook-driven cascades (installation.deleted) typically pass
// false to avoid a feedback loop.
func (s *OrgDisconnectService) Disconnect(ctx context.Context, ocOrgID, cause string, uninstallApp bool) error {
	if cause == "" {
		cause = "org.disconnected"
	}
	// Phase A — confirm the row exists. If not, return ErrOrgNotFound so
	// the controller can return 200 idempotent.
	proj, err := s.credSvc.Status(ctx, ocOrgID)
	if err != nil {
		var nfe *NotFoundError
		if errors.As(err, &nfe) {
			return ErrOrgNotFound
		}
		return fmt.Errorf("disconnect Phase A: status: %w", err)
	}
	slog.InfoContext(ctx, "disconnect: starting cascade", "ocOrgId", ocOrgID, "kind", proj.Kind, "status", proj.Status)
	if proj.Status == "disconnected" {
		// Already finalized — nothing to do.
		slog.InfoContext(ctx, "disconnect: already disconnected", "ocOrgId", ocOrgID)
		return nil
	}

	// Phase D — finalize on git-service: status flip + OpenBao GC.
	if err := s.credSvc.Disconnect(ctx, ocOrgID); err != nil {
		var nfe *NotFoundError
		if errors.As(err, &nfe) {
			slog.InfoContext(ctx, "disconnect: already finalized during cascade", "ocOrgId", ocOrgID)
			return nil
		}
		return fmt.Errorf("disconnect Phase D: %w", err)
	}

	// Phase E — best-effort GitHub-side uninstall. App-mode only; PAT and
	// failure are silent (the platform row is gone regardless, and an
	// admin can clean up via github.com if needed).
	if uninstallApp && proj.Kind == "app-installation" {
		if err := s.credSvc.UninstallAppInstallation(ctx, ocOrgID); err != nil {
			slog.WarnContext(ctx, "disconnect Phase E: uninstall failed", "ocOrgId", ocOrgID, "error", err)
		}
	}

	// Phase F — best-effort disk cleanup: rename the org's whole workspace
	// subtree (all projects incl. _skills) into trash. The hook logs its own
	// failures and never fails the cascade; a missed trash here just leaves
	// dirs whose git_repositories rows still exist, and the disconnected org
	// has no credential to re-fetch with — the reaper's quota/LRU pass
	// eventually reclaims the cold mirrors.
	if s.workspaceTrash != nil {
		s.workspaceTrash(ctx, ocOrgID)
	}

	slog.InfoContext(ctx, "disconnect: cascade complete", "ocOrgId", ocOrgID, "uninstallApp", uninstallApp)
	return nil
}
