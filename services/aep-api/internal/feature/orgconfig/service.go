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

// service.go — the Service orchestrator: it assembles GET /config from the
// three underlying services and runs the atomic, probe-before-persist PATCH.
// It owns no storage — every read/write delegates to the reused orgcreds/idp
// services; the only new logic here is the cross-section sequencing.

package orgconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/oidc"
	"github.com/wso2/aep/aep-api/internal/feature/idp"
	"github.com/wso2/aep/aep-api/internal/feature/orgcreds"
	"github.com/wso2/aep/aep-api/models"
)

// ErrGitHubAppNotConfigured is returned by StartGitHubConnect when the GitHub
// App OAuth client isn't wired on this deployment (the App-mode connect path is
// unavailable). Mapped to 503 at the HTTP edge.
var ErrGitHubAppNotConfigured = errors.New("orgconfig: github app oauth client not configured")

// SectionError is a per-section failure carrying the RFC-9457 location pointer
// (body.<section>) the console uses to highlight the offending form section. It
// is produced by PATCH probe/persist failures; the HTTP layer maps Status +
// Section into a problem response.
type SectionError struct {
	Section string // "llm" | "gitProvider" | "idp"
	Status  int    // 422 (validation) | 409 (conflict) | 502 (upstream)
	Message string
}

func (e *SectionError) Error() string { return "body." + e.Section + ": " + e.Message }

// sectionErrorFrom classifies a reused-service error into a SectionError with
// the right status: a section-field validation failure is a 422 pointing at the
// section, a cross-mode conflict a 409, an upstream 5xx a 502. An unclassified
// error is returned verbatim (the caller maps it to an opaque 500).
func sectionErrorFrom(section string, err error) error {
	var ve *orgcreds.ValidationError
	if errors.As(err, &ve) {
		return &SectionError{Section: section, Status: http.StatusUnprocessableEntity, Message: ve.Error()}
	}
	var ce *orgcreds.ConflictError
	if errors.As(err, &ce) {
		return &SectionError{Section: section, Status: http.StatusConflict, Message: ce.Error()}
	}
	var ue *orgcreds.UpstreamError
	if errors.As(err, &ue) {
		return &SectionError{Section: section, Status: http.StatusBadGateway, Message: ue.Error()}
	}
	return err
}

// Service is the /config orchestrator. It holds the reused services + the
// platform IDP defaults (used to synthesize a not-yet-persisted idp section on
// GET) + the GitHub App connect parameters.
type Service struct {
	anthropicSvc  *orgcreds.AnthropicCredentialService
	credentialSvc *orgcreds.CredentialService
	disconnectSvc *orgcreds.OrgDisconnectService
	bearerSvc     *orgcreds.BearerService
	idpSvc        idp.IDPService
	platformIDP   idp.PlatformIDPConfig

	publicURL   string
	appClientID string
}

// NewService wires the orchestrator. The defaulting for publicURL mirrors the
// legacy NewOrgGitHubController so the connect-session redirect_uri is
// identical. Any dependency may be nil in narrow test harnesses that exercise
// only a subset of sections; each handler nil-guards what it needs.
func NewService(
	anthropicSvc *orgcreds.AnthropicCredentialService,
	credentialSvc *orgcreds.CredentialService,
	disconnectSvc *orgcreds.OrgDisconnectService,
	bearerSvc *orgcreds.BearerService,
	idpSvc idp.IDPService,
	platformIDP idp.PlatformIDPConfig,
	publicURL, appClientID string,
) *Service {
	if publicURL == "" {
		publicURL = "http://localhost:8090"
	}
	return &Service{
		anthropicSvc:  anthropicSvc,
		credentialSvc: credentialSvc,
		disconnectSvc: disconnectSvc,
		bearerSvc:     bearerSvc,
		idpSvc:        idpSvc,
		platformIDP:   platformIDP,
		publicURL:     publicURL,
		appClientID:   appClientID,
	}
}

// --- GET /config ------------------------------------------------------------

// Get assembles the full config projection for org. A missing llm/gitProvider
// row maps to a null section (not an error); idp is always present, synthesized
// from the platform defaults when no row exists yet so GET stays side-effect
// free (no row is created on read).
func (s *Service) Get(ctx context.Context, org string) (*models.ConfigProjection, error) {
	out := &models.ConfigProjection{}

	if s.anthropicSvc != nil {
		proj, err := s.anthropicSvc.Status(ctx, org)
		switch {
		case err == nil:
			out.LLM = llmProjectionFrom(proj)
		case isNotFound(err):
			out.LLM = nil
		default:
			return nil, fmt.Errorf("orgconfig get llm: %w", err)
		}
	}

	if s.credentialSvc != nil {
		proj, err := s.credentialSvc.Status(ctx, org)
		switch {
		// A disconnected row is retained by the disconnect cascade (audit trail,
		// app re-adoption) but the config contract says null = not connected —
		// projecting it would keep the console's onboarding gate (ADR-0009) and
		// settings card treating the org as connected.
		case err == nil && proj.Status != "disconnected":
			out.GitProvider = gitProviderProjectionFrom(proj)
		case err == nil || isNotFound(err):
			out.GitProvider = nil
		default:
			return nil, fmt.Errorf("orgconfig get gitProvider: %w", err)
		}
	}

	out.IDP = s.idpProjection(ctx, org)
	return out, nil
}

// idpProjection returns the org's persisted IDP profile, or the platform
// default (kind=platform + cluster issuer/jwks) when none exists yet. Read-only
// — unlike UpdateProfile's GetOrCreateProfile, it never persists on GET.
func (s *Service) idpProjection(ctx context.Context, org string) models.IDPProjection {
	if s.idpSvc != nil {
		if profile, err := s.idpSvc.GetProfile(ctx, org); err == nil && profile != nil {
			return idpProjectionFrom(profile)
		} else if err != nil {
			slog.WarnContext(ctx, "orgconfig: idp GetProfile failed; falling back to platform default",
				"org", org, "error", err)
		}
	}
	return models.IDPProjection{
		Kind:    "platform",
		Issuer:  s.platformIDP.Issuer,
		JWKSURL: s.platformIDP.JWKSURL,
	}
}

// --- PATCH /config ----------------------------------------------------------

// Patch applies a config patch atomically: it first rejects the disallowed null
// spellings, then probes every sent-with-value section that has an external
// probe, and only if ALL probes pass does it persist. A probe failure returns a
// SectionError (422/409/502 body.<section>) with nothing written, so a bad key
// in one section can't leave another section half-applied. Returns the
// post-write projection (equal to an immediately following GET).
func (s *Service) Patch(ctx context.Context, org, actor string, p models.ConfigPatch) (*models.ConfigProjection, error) {
	// 1. Reject the null spellings that have a dedicated action / reset instead
	//    (Decision 7). llm:null is allowed (it clears).
	if p.GitProvider.Sent && p.GitProvider.Null {
		return nil, &SectionError{
			Section: "gitProvider", Status: http.StatusUnprocessableEntity,
			Message: "use POST /config/git-provider/disconnect to disconnect the git provider",
		}
	}
	if p.IDP.Sent && p.IDP.Null {
		return nil, &SectionError{
			Section: "idp", Status: http.StatusUnprocessableEntity,
			Message: `an org always has an IDP; reset it with {"kind":"platform"}`,
		}
	}

	// 2. Probe phase — no writes. Any failure aborts the whole patch.
	if p.LLM.Sent && !p.LLM.Null {
		if s.anthropicSvc == nil {
			return nil, fmt.Errorf("orgconfig patch llm: service not configured")
		}
		if err := s.anthropicSvc.ValidateKey(ctx, p.LLM.Value.APIKey); err != nil {
			return nil, sectionErrorFrom("llm", err)
		}
	}
	if p.GitProvider.Sent && !p.GitProvider.Null {
		if s.credentialSvc == nil {
			return nil, fmt.Errorf("orgconfig patch gitProvider: service not configured")
		}
		if err := s.credentialSvc.ValidatePAT(ctx, p.GitProvider.Value.PAT, p.GitProvider.Value.GitHubLogin); err != nil {
			return nil, sectionErrorFrom("gitProvider", err)
		}
	}

	// 3. Persist phase — probes already passed, so these are writes over
	//    freshly-validated inputs. Ordered llm → gitProvider → idp.
	sections := []string{}
	if p.LLM.Sent {
		if p.LLM.Null {
			if err := s.anthropicSvc.Disconnect(ctx, org); err != nil {
				return nil, sectionErrorFrom("llm", err)
			}
		} else if _, err := s.anthropicSvc.Connect(ctx, org, orgcreds.AnthropicConnectRequest{APIKey: p.LLM.Value.APIKey}); err != nil {
			return nil, sectionErrorFrom("llm", err)
		}
		sections = append(sections, "llm")
	}
	if p.GitProvider.Sent && !p.GitProvider.Null {
		if _, err := s.credentialSvc.Connect(ctx, org, orgcreds.ConnectRequest{
			Kind:        "user-pat",
			PAT:         p.GitProvider.Value.PAT,
			GitHubLogin: p.GitProvider.Value.GitHubLogin,
		}); err != nil {
			return nil, sectionErrorFrom("gitProvider", err)
		}
		sections = append(sections, "gitProvider")
	}
	if p.IDP.Sent && !p.IDP.Null {
		if s.idpSvc == nil {
			return nil, fmt.Errorf("orgconfig patch idp: service not configured")
		}
		if _, err := s.idpSvc.SetProfile(ctx, org, actor, p.IDP.Value.Kind, p.IDP.Value.Issuer, p.IDP.Value.JWKSURL); err != nil {
			return nil, sectionErrorFrom("idp", err)
		}
		sections = append(sections, "idp")
	}

	// Audit which sections were carried — never the secret values (Decision:
	// coarser RBAC compensated by section-level audit logging).
	slog.InfoContext(ctx, "orgconfig.patched", "org", org, "sections", sections)

	return s.Get(ctx, org)
}

// --- Action routes ----------------------------------------------------------

// StartGitHubConnect mints a connect-state JWT and returns the GitHub App OAuth
// authorize URL. Mirrors the legacy start-github-connect handler exactly (same
// state issuance, same redirect_uri built from the unchanged callback path).
func (s *Service) StartGitHubConnect(ctx context.Context, org, actor string, installationID int64) (string, error) {
	if s.appClientID == "" {
		return "", ErrGitHubAppNotConfigured
	}
	state, err := s.bearerSvc.IssueConnectState(org, actor, installationID, 15*time.Minute)
	if err != nil {
		return "", fmt.Errorf("orgconfig start connect: %w", err)
	}
	redirectURI := s.publicURL + orgcreds.ConnectCallbackPath
	authorizeURL := "https://github.com/login/oauth/authorize?client_id=" + url.QueryEscape(s.appClientID) +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&state=" + url.QueryEscape(state)
	return authorizeURL, nil
}

// DisconnectGitProvider runs the disconnect cascade. It returns whether a
// connection existed (false → the caller reports an idempotent not_connected).
func (s *Service) DisconnectGitProvider(ctx context.Context, org string, uninstall bool) (bool, error) {
	if err := s.disconnectSvc.Disconnect(ctx, org, "manual.disconnect", uninstall); err != nil {
		if errors.Is(err, orgcreds.ErrOrgNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("orgconfig disconnect: %w", err)
	}
	return true, nil
}

// RotateIDPClientSecret mints a fresh publisher client secret (returned once).
func (s *Service) RotateIDPClientSecret(ctx context.Context, org, actor string) (string, error) {
	return s.idpSvc.RegenerateClientSecret(ctx, org, actor)
}

// DiscoverIDP resolves an OIDC issuer's discovery document.
func (s *Service) DiscoverIDP(ctx context.Context, issuer string) (issuerOut, jwksURL string, err error) {
	md, err := oidc.DiscoverFromIssuer(ctx, issuer)
	if err != nil {
		return "", "", err
	}
	return md.Issuer, md.JWKSURI, nil
}

// --- projection mappers -----------------------------------------------------

func llmProjectionFrom(p *orgcreds.AnthropicProjection) *models.LLMProjection {
	if p == nil {
		return nil
	}
	return &models.LLMProjection{
		Kind:            "anthropic",
		KeyPrefix:       p.KeyPrefix,
		KeyLast4:        p.KeyLast4,
		Status:          p.Status,
		ConnectedAt:     p.ConnectedAt,
		LastValidatedAt: p.LastValidatedAt,
		ValidationError: p.ValidationError,
	}
}

func gitProviderProjectionFrom(p *orgcreds.Projection) *models.GitProviderProjection {
	if p == nil {
		return nil
	}
	return &models.GitProviderProjection{
		Kind:              "github",
		Mode:              gitProviderMode(p.Kind),
		GitHubLogin:       p.GitHubLogin,
		IdentityLogin:     p.IdentityLogin,
		IdentityName:      p.IdentityName,
		IdentityEmail:     p.IdentityEmail,
		InstallationID:    p.InstallationID,
		SelectedRepos:     p.SelectedRepos,
		Status:            p.Status,
		ConnectedAt:       p.ConnectedAt,
		LastValidatedAt:   p.LastValidatedAt,
		IdentityChangedAt: p.IdentityChangedAt,
		PrevIdentityLogin: p.PrevIdentityLogin,
	}
}

// gitProviderMode re-expresses the legacy credential kind as the role-named
// mode the config surface exposes.
func gitProviderMode(kind string) string {
	switch kind {
	case "app-installation":
		return "app"
	case "user-pat":
		return "pat"
	default:
		return kind
	}
}

func idpProjectionFrom(p *models.OrganizationIDPProfile) models.IDPProjection {
	return models.IDPProjection{
		Kind:              p.Kind,
		Issuer:            p.Issuer,
		JWKSURL:           p.JWKSURL,
		PublisherClientID: p.PublisherClientID,
		HasClientSecret:   p.PublisherClientSecret != "",
	}
}

func isNotFound(err error) bool {
	var nfe *orgcreds.NotFoundError
	return errors.As(err, &nfe)
}
