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

// Package genai is the BFF's committed-truth LLM turn surface. A turn is
// started with POST …/turns → 202 {turnId} and runs detached server-side: the
// runner snapshots the project repo + the org's _skills repo onto the shared
// workspace volume, dispatches the agents service with a WorkspaceRef (IDs +
// shas — no file content on the wire), taps the SSE stream into an in-memory
// broker (resumable GET …/turns/{id}/stream), folds the file mutations in Go
// (internal/platform/agentfold), gates the fold on the agents-side manifest,
// and commits the result straight to main. The durable agent_turns row is the
// one-active-turn-per-project guard and the crash-safety anchor.
package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/agentsvc"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// Valid use cases (the FE-supplied "useCase" field).
const (
	useCaseRequirementsGenerate = "requirements-generate"
	useCaseRequirementsChat     = "requirements-chat"
	useCaseDesignGenerate       = "design-generate"
	// useCaseGeneral is the INTERNAL use case a turn runs under when the client
	// omits "useCase" — a generic spec turn with generic steering, a generic
	// commit message, and no requirements/design gating. It is not a client
	// value (absent from validUseCases): the edge reaches it only by normalizing
	// an absent field, so the wire enum stays the three real flows.
	useCaseGeneral = "general"
)

var validUseCases = map[string]bool{
	useCaseRequirementsGenerate: true,
	useCaseRequirementsChat:     true,
	useCaseDesignGenerate:       true,
}

// Errors — pre-202, mapped to HTTP status by the huma layer.
var (
	ErrProjectRepoNotFound   = errors.New("project repository not found")
	ErrInvalidUseCase        = errors.New("invalid use case")
	ErrInvalidConversationID = errors.New("invalid conversation id")
	ErrEmptyInstruction      = errors.New("instruction must not be empty")
	// ErrCollabNoToken rejects a room-scoped turn whose request carried no
	// bearer — the agent joins the room with the caller's token (#86 d7).
	ErrCollabNoToken  = errors.New("collab turn requires a bearer token")
	ErrNoAnthropicKey = errors.New("organization has no Anthropic API key configured")
	// ErrRequirementsMissing gates design generation on requirements CONTENT
	// at HEAD (single-tag build flow: the version tag is cut by the build
	// endpoint AFTER the design exists, so a tag can never be a precondition
	// for generating the first design).
	ErrRequirementsMissing  = errors.New("design generation requires requirements content — write or generate requirements first")
	ErrConversationNotFound = errors.New("conversation not found")
	ErrTurnNotFound         = errors.New("turn not found")
	// ErrSkillsRepoUnavailable means the org's _skills repo (the turn's
	// SkillsRef source) could not be resolved — its row is missing or
	// unprovisionable, or the backing repo is gone/unreachable (live incident:
	// the GitHub repo was deleted externally while its git_repositories row
	// lingered). Mapped to a LOGGED 503 with a clear message instead of the
	// generic 500 that previously swallowed the cause. Wraps the underlying
	// error for the logs. Recovery is a manual operator action today: delete
	// the stale `_skills` git_repositories row — the next resolve re-provisions
	// and re-seeds the repo.
	ErrSkillsRepoUnavailable = errors.New("org skills repository unavailable")
)

// TurnInProgressError is the D18 guard rejection: another turn holds the
// project's one-active slot. The huma layer renders it as
// 409 {"code":"turn_in_progress","activeTurnId":…} so the FE can attach to
// the running stream as a viewer.
type TurnInProgressError struct {
	ActiveTurnID string
}

func (e *TurnInProgressError) Error() string {
	return "a turn is already running for this project (active turn " + e.ActiveTurnID + ")"
}

// conversationIDPattern bounds the FE-chosen id to a safe shape. A "--"
// substring is additionally rejected (see validConversationID) so the FE can
// never inject the namespace separator and escape its tenant scope.
var conversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,200}$`)

// requirementsTagPattern matches a requirements version tag `v<N>` (design
// tags are `v<N>-<M>` and are deliberately excluded — approval is the
// requirements version, not a design revision).
var requirementsTagPattern = regexp.MustCompile(`^v(\d+)$`)

// ---- ports -----------------------------------------------------------------

// RepoResolver looks up the project's git repo row (its OrgID/ProjectID are the
// authenticated tenant scope woven into the namespaced conversation id).
type RepoResolver interface {
	GetRepo(ctx context.Context, orgID, projectID string) (*sourcecontrol.GitRepository, error)
}

// GitReader is the workspace-backed git surface the turn flow drives: reads +
// the commit path (Workspace), per-op credentials (Resolver), and the save
// identity helper. sourcecontrol.GitOpsService satisfies it.
type GitReader interface {
	Workspace() sourcecontrol.Workspace
	Resolver() secrets.Resolver
	ResolveSaveIdentities(cred secrets.Credential) (*sourcecontrol.GitIdentity, *sourcecontrol.GitIdentity)
}

// AnthropicKeyResolver resolves the effective org Anthropic key. An empty key
// with a nil error means "org has none" → the service raises ErrNoAnthropicKey
// pre-202 (no platform fallback). Wired from AnthropicCredentialService.
type AnthropicKeyResolver func(ctx context.Context, orgID string) (string, error)

// SkillsRepoResolver ensures the org's _skills repo is provisioned (seeded
// with the embedded flow skills) and returns its row — the source of the
// turn's SkillsRef snapshot. Wired at the composition root from the skills
// feature (EnsureProvisioned + GetRepo) so genai holds no skills edge.
type SkillsRepoResolver func(ctx context.Context, orgID string) (*sourcecontrol.GitRepository, error)

// ---- input / views ----------------------------------------------------------

// TurnInput is the assembled turn request (conversationId from the path, the
// rest from the body). There is no Files field — the turn input is the
// committed snapshot at HEAD (D13), and filesChangedExternally is
// server-derived (D20).
type TurnInput struct {
	UseCase        string
	ConversationID string
	Instruction    string
	Target         string
	// Collab makes this a room-scoped turn (#86 phase 4): the agents service
	// joins the project's spec room as a live Yjs peer with the prompting
	// user's bearer, edits the shared doc, and nothing is committed to git.
	Collab bool
}

// TurnStatus is the read view of one turn (the status GET body).
type TurnStatus struct {
	TurnID         string    `json:"turnId"`
	ConversationID string    `json:"conversationId"`
	UseCase        string    `json:"useCase"`
	Status         string    `json:"status"`
	CommitSHA      string    `json:"commitSha,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Paths          []string  `json:"paths,omitempty"`
	NoChanges      bool      `json:"noChanges,omitempty"`
	Message        string    `json:"message,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func turnStatusOf(t *AgentTurn) *TurnStatus {
	return &TurnStatus{
		TurnID:         t.ID,
		ConversationID: t.ConversationID,
		UseCase:        t.UseCase,
		Status:         t.Status,
		CommitSHA:      t.CommitSHA,
		Reason:         t.Reason,
		Paths:          decodePaths(t.Paths),
		NoChanges:      t.NoChanges,
		Message:        t.Message,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}
}

// ---- service ---------------------------------------------------------------

// MCPTokenMinter mints a short-lived BFF-signed identity token (aud
// aep-api-mcp) carrying orgID, for the agents service to call back into the
// BFF's internal MCP discovery surface on a generation turn.
// *auth.TaskTokenManager satisfies it via IssueMCPToken; defined here (consumer
// side) so genai needn't import the platform/auth package.
type MCPTokenMinter interface {
	IssueMCPToken(orgID string) (string, error)
}

// ServiceDeps wires the genai service. Repos..SkillsRepo are required; MCPTokens
// + MCPBaseURL are optional (dependency-management Phase 5) — when both are set,
// design-generation turns carry a BFF-minted `mcp: {url, token}` block so the
// architect can discover org endpoints / external resources / resource types.
type ServiceDeps struct {
	Repos      RepoResolver
	Git        GitReader
	Keys       AnthropicKeyResolver
	Client     agentsvc.Client
	Turns      TurnRepository
	Broker     *TurnBroker
	Snapshots  sourcecontrol.SnapshotProvider
	SkillsRepo SkillsRepoResolver
	MCPTokens  MCPTokenMinter
	MCPBaseURL string
	// TurnFinishHook, when set, is invoked once with the terminal outcome of
	// every turn (outcome is "completed" | "failed"). The devflow feature uses
	// it to signal a waiting design-generate workflow. Best-effort — a nil hook
	// or a slow hook must never delay or fail the turn. Kept as a plain func so
	// the genai package does not import devflow.
	TurnFinishHook func(ctx context.Context, orgID, projectID, turnID, useCase, outcome string)
}

// Service is the typed entry point behind the turn/status/stream/rehydrate
// endpoints. edge.Deps holds it as a concrete *genai.Service — there is one
// implementation and no test fake (the old GenAIService interface had no
// substitution; the component tier exercises the real service).
type Service struct {
	repos      RepoResolver
	git        GitReader
	keys       AnthropicKeyResolver
	client     agentsvc.Client
	turns      TurnRepository
	broker     *TurnBroker
	snapshots  sourcecontrol.SnapshotProvider
	skillsRepo SkillsRepoResolver
	mcpTokens  MCPTokenMinter
	mcpBaseURL string
	finishHook func(ctx context.Context, orgID, projectID, turnID, useCase, outcome string)
}

// NewService wires the genai service.
func NewService(d ServiceDeps) *Service {
	return &Service{
		repos:      d.Repos,
		git:        d.Git,
		keys:       d.Keys,
		client:     d.Client,
		turns:      d.Turns,
		broker:     d.Broker,
		snapshots:  d.Snapshots,
		skillsRepo: d.SkillsRepo,
		mcpTokens:  d.MCPTokens,
		mcpBaseURL: d.MCPBaseURL,
		finishHook: d.TurnFinishHook,
	}
}

func (s *Service) StartTurn(ctx context.Context, orgID, projectID string, in TurnInput) (string, error) {
	// useCase is optional. An ABSENT value runs the generic turn (useCaseGeneral):
	// generic steering + commit message, no requirements/design gate. A PRESENT
	// value must be one of the three real flows (the wire enum already fences
	// HTTP callers; this guards direct service/test callers too).
	if in.UseCase != "" && !validUseCases[in.UseCase] {
		return "", ErrInvalidUseCase
	}
	useCase := in.UseCase
	if useCase == "" {
		useCase = useCaseGeneral
	}
	if !validConversationID(in.ConversationID) {
		return "", ErrInvalidConversationID
	}
	// A blank/whitespace-only instruction can never produce a turn — reject it
	// synchronously (pre-202, no row, guard untaken) instead of letting it fail
	// post-dispatch as an opaque "dispatch-failed".
	if strings.TrimSpace(in.Instruction) == "" {
		return "", ErrEmptyInstruction
	}
	repo, err := s.resolveRepo(ctx, orgID, projectID)
	if err != nil {
		return "", err
	}
	ref, err := sourcecontrol.ResolveWorkspaceRef(ctx, s.git.Resolver(), orgID, repo)
	if err != nil {
		return "", fmt.Errorf("resolve workspace ref: %w", err)
	}

	specTag := ""

	key, err := s.resolveKey(ctx, orgID)
	if err != nil {
		return "", err
	}

	// D20: identities are captured NOW — the turn runs detached, with no
	// request context left to consult. Author = the prompting user (JWT
	// subject + a noreply address; Thunder claims carry no email), committer
	// = the org credential's save identity (the AEP bot fallback).
	author, committer := s.captureIdentities(ctx, ref)

	// Room-scoped turn (#86 phase 4): capture the room + the prompting user's
	// bearer NOW (D20 — the runner has no request context). Access is
	// request-scoped: the collab server's oracle validates this token exactly
	// like a browser join; no token → the turn cannot join, fail pre-202.
	collabRoomID, collabToken := "", ""
	if in.Collab {
		collabToken = auth.GetAuthToken(ctx)
		if collabToken == "" {
			return "", ErrCollabNoToken
		}
		collabRoomID = "spec-" + orgID + "-" + projectID
	}

	// Collab room-scoped turns author design.json live under the
	// requirements-chat useCase, whose steering never names the design flow —
	// append the dependency-discovery steer so the collab architect loads the
	// architecture skill and resolves real provider names via list_org_endpoints
	// (the MCP block is attached in mcpForTurn) instead of inventing role-based
	// org-service names that fail exact-name resolution at build.
	collabSteer := ""
	if in.Collab {
		collabSteer = collabDepsSteer
	}

	ws := s.git.Workspace()
	baseRef, err := ws.Head(ctx, ref, "")
	if err != nil {
		return "", fmt.Errorf("resolve base ref: %w", err)
	}

	// Design gate (single-tag build flow): requirements CONTENT must exist at
	// HEAD — generating a design from an empty spec is always a user error.
	// The old D19 approve-first gate (a v<N> tag as precondition) is gone: the
	// build endpoint cuts the tag AFTER the design exists, so requiring one
	// here deadlocked every new project. The latest v<N>, when one exists,
	// still stamps the turn's lineage specTag ("" on a first build).
	if useCase == useCaseDesignGenerate {
		if err := s.requireRequirementsContent(ctx, ref, baseRef); err != nil {
			return "", err
		}
		if specTag, err = s.latestRequirementsTag(ctx, ref); err != nil {
			return "", err
		}
	}

	// Skills resolve failures are typed: both arms mean the org's _skills repo
	// is unusable right now (row missing/unprovisionable, or the backing repo
	// gone/unreachable — e.g. deleted externally under a lingering row). The
	// edge maps this to a logged 503 rather than the old opaque 500.
	skillsRow, err := s.skillsRepo(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("%w: resolve repo row: %w", ErrSkillsRepoUnavailable, err)
	}
	skillsRepoRef := sourcecontrol.WorkspaceRefFor(orgID, skillsRow, ref.Cred)
	skillsRef, err := ws.Head(ctx, skillsRepoRef, "")
	if err != nil {
		return "", fmt.Errorf("%w: resolve head: %w", ErrSkillsRepoUnavailable, err)
	}
	if err := s.snapshots.Ensure(ctx, ref, baseRef); err != nil {
		return "", fmt.Errorf("ensure repo snapshot: %w", err)
	}
	if err := s.snapshots.Ensure(ctx, skillsRepoRef, skillsRef); err != nil {
		return "", fmt.Errorf("ensure skills snapshot: %w", err)
	}

	// D18 guard: one active turn per project, any use case.
	row, err := s.turns.TryStart(ctx, &AgentTurn{
		OrgID:          orgID,
		ProjectID:      projectID,
		ConversationID: in.ConversationID,
		UseCase:        useCase,
		BaseRef:        baseRef,
		SkillsRef:      skillsRef,
		Status:         turnStatusRunning,
		SpecTag:        specTag,
	})
	if errors.Is(err, ErrTurnActive) {
		return "", &TurnInProgressError{ActiveTurnID: row.ID}
	}
	if err != nil {
		return "", fmt.Errorf("start turn: %w", err)
	}
	s.broker.Open(row.ID)

	job := turnJob{
		turnID:           row.ID,
		orgID:            orgID,
		projectID:        projectID,
		useCase:          useCase,
		conversationID:   in.ConversationID,
		nsConversationID: namespacedID(repo, useCase, in.ConversationID),
		instruction:      in.Instruction + steeringByUseCase[useCase] + collabSteer + targetSuffix(in.Target),
		summary:          in.Instruction,
		repoRef:          ref,
		baseRef:          baseRef,
		skillsRef:        skillsRef,
		anthropicKey:     key,
		author:           author,
		committer:        committer,
		collabRoomID:     collabRoomID,
		collabToken:      collabToken,
	}
	// Detached: the turn runs to completion (or a terminal failure) server-
	// side regardless of the client connection (D16). runTurnSafe is the panic
	// barrier so a fold/parser panic fails just this turn, not the process.
	go s.runTurnSafe(context.WithoutCancel(ctx), job)
	return row.ID, nil
}

func (s *Service) TurnStatus(ctx context.Context, orgID, projectID, turnID string) (*TurnStatus, error) {
	t, err := s.turns.Get(ctx, orgID, projectID, turnID)
	if err != nil {
		return nil, fmt.Errorf("get turn: %w", err)
	}
	if t == nil {
		return nil, ErrTurnNotFound
	}
	return turnStatusOf(t), nil
}

func (s *Service) ActiveTurn(ctx context.Context, orgID, projectID string) (*TurnStatus, error) {
	t, err := s.turns.GetActive(ctx, orgID, projectID)
	if err != nil {
		return nil, fmt.Errorf("get active turn: %w", err)
	}
	if t == nil {
		return nil, nil
	}
	return turnStatusOf(t), nil
}

func (s *Service) AttachTurn(ctx context.Context, orgID, projectID, turnID string, from int) (*TurnSubscription, error) {
	// Tenant fence first: the broker is keyed by turn id alone, so ownership
	// is checked against the durable row before any buffer is exposed.
	t, err := s.turns.Get(ctx, orgID, projectID, turnID)
	if err != nil {
		return nil, fmt.Errorf("get turn: %w", err)
	}
	if t == nil {
		return nil, ErrTurnNotFound
	}
	sub, err := s.broker.Subscribe(turnID, from)
	if errors.Is(err, ErrTurnNotBuffered) {
		// Known turn, expired (or other-replica) buffer → 404 pre-stream; the
		// FE falls back to the status GET.
		return nil, ErrTurnNotFound
	}
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *Service) Rehydrate(ctx context.Context, orgID, projectID, conversationID string) (json.RawMessage, error) {
	if !validConversationID(conversationID) {
		return nil, ErrInvalidConversationID
	}
	repo, err := s.resolveRepo(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	// Rehydrate is chat-only (single-turn generate flows never rehydrate). The
	// console omits "useCase", so its turns are namespaced under useCaseGeneral;
	// reconstruct the id under the same use case the write path stored it with.
	convID := namespacedID(repo, useCaseGeneral, conversationID)
	raw, err := s.client.GetConversation(ctx, convID, orgID)
	if err != nil {
		var ue *agentsvc.UpstreamError
		if errors.As(err, &ue) && ue.StatusCode == 404 {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return raw, nil
}

func (s *Service) resolveRepo(ctx context.Context, orgID, projectID string) (*sourcecontrol.GitRepository, error) {
	if s == nil || s.repos == nil {
		return nil, ErrProjectRepoNotFound
	}
	repo, err := s.repos.GetRepo(ctx, orgID, projectID)
	if err != nil {
		if errors.Is(err, sourcecontrol.ErrRepoNotFound) {
			return nil, ErrProjectRepoNotFound
		}
		return nil, fmt.Errorf("resolve project repo: %w", err)
	}
	if repo == nil {
		return nil, ErrProjectRepoNotFound
	}
	// A repo that is not ready yet (still provisioning / errored) cannot back
	// a snapshot — surface it as not-found rather than dispatching a turn
	// that would fail at the mirror.
	if repo.Status != "" && repo.Status != "ready" {
		return nil, ErrProjectRepoNotFound
	}
	return repo, nil
}

func (s *Service) resolveKey(ctx context.Context, orgID string) (string, error) {
	if s.keys == nil {
		return "", ErrNoAnthropicKey
	}
	key, err := s.keys(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("resolve anthropic key: %w", err)
	}
	if key == "" {
		return "", ErrNoAnthropicKey
	}
	return key, nil
}

// latestRequirementsTag lists the v* tags and returns the highest `v<N>` spec
// tag name, or "" when none exists yet (a first-build project) — the design
// turn's lineage stamp, no longer a gate.
func (s *Service) latestRequirementsTag(ctx context.Context, ref sourcecontrol.RepoRef) (string, error) {
	tags, err := s.git.Workspace().ListTags(ctx, ref, "v")
	if err != nil {
		return "", fmt.Errorf("list requirement tags: %w", err)
	}
	bestN := -1
	best := ""
	for _, tag := range tags {
		m := requirementsTagPattern.FindStringSubmatch(tag.Name)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > bestN {
			bestN, best = n, tag.Name
		}
	}
	return best, nil
}

// requireRequirementsContent is the design-generate gate: the requirements
// main doc must be non-empty at the turn's base ref.
func (s *Service) requireRequirementsContent(ctx context.Context, ref sourcecontrol.RepoRef, at string) error {
	const mainDoc = "specs/requirements/requirements.md"
	files, _, err := s.git.Workspace().ReadBundle(ctx, ref, at, func(rel string) bool {
		return rel == mainDoc
	})
	if err != nil {
		return fmt.Errorf("read requirements at %s: %w", at, err)
	}
	if strings.TrimSpace(files[mainDoc]) == "" {
		return ErrRequirementsMissing
	}
	return nil
}

// captureIdentities resolves the D20 commit identities at POST time: author =
// the prompting user from the verified JWT claims (subject + noreply email —
// Thunder claims carry no email/display name), committer = the org
// credential's save identity (falls back to the AEP bot). When no claims are
// present (should not happen on the authed edge), both fall back to the
// committer.
func (s *Service) captureIdentities(ctx context.Context, ref sourcecontrol.RepoRef) (author, committer *sourcecontrol.GitIdentity) {
	_, committer = s.git.ResolveSaveIdentities(ref.Cred)
	author = committer
	if claims := auth.ClaimsFromContext(ctx); claims != nil && claims.Subject != "" {
		author = &sourcecontrol.GitIdentity{
			Name:  claims.Subject,
			Email: noreplyEmail(claims.Subject),
		}
	}
	return author, committer
}

// noreplyEmail derives a stable, valid noreply address from a JWT subject.
func noreplyEmail(subject string) string {
	var b strings.Builder
	for _, r := range subject {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	local := b.String()
	if local == "" {
		local = "aep-user"
	}
	return local + "@users.noreply.aep.dev"
}

// ---- pure helpers ----------------------------------------------------------

// validConversationID enforces the safe shape and rejects the namespace
// separator so a crafted id cannot escape the tenant scope.
func validConversationID(id string) bool {
	return conversationIDPattern.MatchString(id) && !strings.Contains(id, "--")
}

// namespacedID scopes the FE-supplied conversation uuid to the authenticated
// tenant + use case via the shared agentsvc encoding. The FE never sees the
// namespaced id; validConversationID rejects "--" so the uuid cannot forge
// extra segments.
func namespacedID(repo *sourcecontrol.GitRepository, useCase, uuid string) string {
	return agentsvc.ConversationID(repo.OrgID, repo.ProjectID, useCase, uuid)
}

func targetSuffix(target string) string {
	if strings.TrimSpace(target) == "" {
		return ""
	}
	return "\n\n(target: " + target + ")"
}
