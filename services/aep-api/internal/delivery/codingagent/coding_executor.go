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

package codingagent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wso2/aep/aep-api/internal/organization"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
)

// CodingExecutor launches the coding agent. Its one dispatch entry point is
// Dispatch (milestone_dispatch.go), the run supervisor's
// delivery.MilestoneDispatcher: one cycle of a milestone run, one agent pod. It
// also owns the build-side retry the exec watcher asks for
// (RetryAuthFailedBuild), which is why it still holds the build-secret stager
// and the executions repository.
//
// Two dispatch paths:
//   - the cluster-gateway-proxy path (per-org NS + per-run ExternalSecrets + a
//     K8s Job), used when the proxy dispatcher + the org's SM-API triplets are
//     configured — this is what the cloud / local-proxy plane exercises (`ca-…` jobs);
//   - the direct K8s Job fallback (K8sJobDispatcher) when the proxy path is not
//     configured — the aep-init local-install path.
type CodingExecutor struct {
	oc            openchoreo.ComponentClient
	repos         ProjectRepos
	identities    Identities
	anthropic     AnthropicProvisioner
	tokens        TokenIssuer
	execRows      delivery.ExecutionRepository
	gitServiceURL string
	platformURL   string

	// Proxy dispatch (nil → fall through to the direct k8sJob path).
	proxy *Dispatcher

	// Org-scoped repository reads, always wired at the composition root: the
	// per-org Anthropic/GitHub SM-API triplets + IDP publisher profile for the
	// proxy dispatch, and the org lookup for the data-plane UUID.
	orgs           organization.OrganizationRepository
	anthropicCreds organization.OrgAnthropicRepository
	githubCreds    organization.OrgCredentialRepository
	idpProfiles    organization.IDPRepository

	// k8sJob is the direct K8s Job dispatch path — the sole fallback when the
	// proxy path is not configured (nil → no fallback; dispatch errors out).
	// Requires the org repository to be set (for org UUID lookup).
	k8sJob             *K8sJobDispatcher
	idp                OrgPublisherProvisioner
	runnerImage        string
	clusterSecretStore string

	// Build-secret staging (nil → unauthenticated clone, correct for public
	// repos). buildSecrets pre-stages the org's build git credential so a build's
	// checkout-source step can clone a private repo; authRetryBudget bounds the
	// git-clone-auth re-mint retries (§7).
	buildSecrets    BuildSecretStager
	authRetryBudget int

	// runnerSecrets resolves the component's external-resource secret bundles so
	// the proxy dispatch mounts them into the runner (nil → none). Best-effort.
	runnerSecrets RunnerSecretResolver

	// skillsRepo resolves (provisioning if needed) the org's `org-skills` repo
	// row so its clone URL can be stamped as AEP_SKILLS_REPO_URL — the runner
	// clones it to resolve the design's applied skills locally (nil → the URL
	// is not stamped and the runner degrades to the base plugin). Best-effort.
	skillsRepo SkillsRepoResolver
}

// SkillsRepoResolver ensures the org's skills repo exists and returns its row.
// Satisfied at the composition root by the same EnsureProvisioned+GetRepo
// closure the genai + task-plan turns use, so this feature grows no skills edge.
type SkillsRepoResolver func(ctx context.Context, orgID string) (*sourcecontrol.GitRepository, error)

// NewCodingExecutor wires the base coding executor. anthropic may be nil. Call
// WithProxy and/or WithK8sJobDispatch to enable a dispatch path.
func NewCodingExecutor(
	oc openchoreo.ComponentClient,
	repos ProjectRepos,
	identities Identities,
	anthropic AnthropicProvisioner,
	tokens TokenIssuer,
	execRows delivery.ExecutionRepository,
	gitServiceURL, platformURL string,
	orgs organization.OrganizationRepository,
	anthropicCreds organization.OrgAnthropicRepository,
	githubCreds organization.OrgCredentialRepository,
	idpProfiles organization.IDPRepository,
) *CodingExecutor {
	return &CodingExecutor{
		oc: oc, repos: repos, identities: identities, anthropic: anthropic,
		tokens: tokens, execRows: execRows, gitServiceURL: gitServiceURL, platformURL: platformURL,
		orgs: orgs, anthropicCreds: anthropicCreds, githubCreds: githubCreds, idpProfiles: idpProfiles,
	}
}

// WithProxy enables the cluster-gateway-proxy coding-agent dispatch path (the
// `ca-…` Job path the local plane uses). runnerImage is THE runner image — one
// image serves both task kinds (it bakes Playwright + chromium), so nothing
// swaps it per kind. idp may be nil (publisher-cc skipped — required only on
// the cloud gateway, i.e. an https platform URL).
func (e *CodingExecutor) WithProxy(proxy *Dispatcher, idp OrgPublisherProvisioner, runnerImage, clusterSecretStore string) *CodingExecutor {
	e.proxy = proxy
	e.idp = idp
	e.runnerImage = runnerImage
	e.clusterSecretStore = clusterSecretStore
	return e
}

// WithK8sJobDispatch enables the direct K8s Job dispatch path. The org UUID
// lookup (needed to derive the data-plane namespace) reads through the org
// repository wired at construction.
func (e *CodingExecutor) WithK8sJobDispatch(d *K8sJobDispatcher) *CodingExecutor {
	e.k8sJob = d
	return e
}

// WithBuildSecrets enables build-secret staging for the post-merge build path
// (private-repo clones) and sets the git-clone-auth retry budget (≤0 → the
// default). stager may be nil (builds clone unauthenticated — correct for public
// repos). Returns the receiver for chained construction.
func (e *CodingExecutor) WithBuildSecrets(stager BuildSecretStager, authRetryBudget int) *CodingExecutor {
	e.buildSecrets = stager
	if authRetryBudget <= 0 {
		authRetryBudget = defaultBuildAuthRetryBudget
	}
	e.authRetryBudget = authRetryBudget
	return e
}

// WithRunnerSecrets enables mounting the component's external-resource secrets
// into the coding runner via per-run ExternalSecrets (nil → none). Returns the
// receiver for chained construction.
func (e *CodingExecutor) WithRunnerSecrets(r RunnerSecretResolver) *CodingExecutor {
	e.runnerSecrets = r
	return e
}

// WithSkillsRepo enables stamping AEP_SKILLS_REPO_URL onto the coding dispatch
// so the runner clones the org's `org-skills` repo to resolve applied skills
// locally (nil → not stamped; the runner degrades to the base plugin). Returns
// the receiver for chained construction.
func (e *CodingExecutor) WithSkillsRepo(r SkillsRepoResolver) *CodingExecutor {
	e.skillsRepo = r
	return e
}

// resolveSkillsRepoURL returns the org's `org-skills` clone URL, provisioning
// the repo on first touch. Best-effort: any failure (no resolver wired,
// provisioning error, missing row) yields "" — the dispatch proceeds without
// the skills URL and the runner degrades to the base plugin rather than failing
// the coding run over a skills-guidance gap.
func (e *CodingExecutor) resolveSkillsRepoURL(ctx context.Context, orgID string) string {
	if e.skillsRepo == nil {
		return ""
	}
	repo, err := e.skillsRepo(ctx, orgID)
	if err != nil || repo == nil {
		slog.WarnContext(ctx, "coding executor: resolve skills repo failed — dispatching without AEP_SKILLS_REPO_URL", "org", orgID, "error", err)
		return ""
	}
	return repo.RepoURL
}

// AuthRetryBudget reports the configured git-clone-auth build retry budget
// (default when unset). The ExecWatcher reads it to bound its retry loop.
func (e *CodingExecutor) AuthRetryBudget() int {
	if e.authRetryBudget <= 0 {
		return defaultBuildAuthRetryBudget
	}
	return e.authRetryBudget
}

// agentLaunch is ONE runner-Job launch with the reason for it stripped out: the
// image, namespace, secrets, tokens and dispatch chain are the same whatever
// asked for the launch, and only the correlation id stamped on the pod and the
// prompt shape differ.
//
// It performs NO state write: a cycle dispatch mints no execution row, because
// the cycle record is the run supervisor'"'"'s own bookkeeping.
type agentLaunch struct {
	orgID     string
	projectID string

	// correlationID is the platform id the pod carries: it is stamped as
	// AEP_TASK_ID, seeds the `ca-…` run name, and is the subject of the runner
	// bearer. It is the dispatching CYCLE'"'"'s id.
	correlationID string

	shape dispatchShape

	// secretComponent, when non-empty, mounts that component'"'"'s external-resource
	// secrets into the runner. It is always empty today: a cycle spans the whole
	// milestone rather than one component, so there is no single component whose
	// secrets to mount.
	secretComponent string

	// repo, when non-nil, is a repository row the caller already resolved (the
	// milestone dispatch reads it to anchor a validation prompt at the issue
	// URL), saving a second lookup. Nil means "resolve it here".
	repo *sourcecontrol.GitRepository
}

// launchAgent resolves the run's credentials and launches the runner Job,
// returning the launched run name. It writes no platform state: everything it
// touches is either a read or the cluster.
func (e *CodingExecutor) launchAgent(ctx context.Context, in agentLaunch) (string, error) {
	repo := in.repo
	if repo == nil {
		resolved, err := e.repos.GetRepo(ctx, in.orgID, in.projectID)
		if err != nil || resolved == nil {
			return "", fmt.Errorf("resolve project repo: %w", err)
		}
		repo = resolved
	}
	name, email, login, err := e.identities.IdentityFor(ctx, in.orgID)
	if err != nil {
		return "", fmt.Errorf("resolve git identity: %w", err)
	}
	bearer, err := e.tokens.Issue(in.correlationID, in.orgID, in.projectID)
	if err != nil {
		return "", fmt.Errorf("mint runner bearer: %w", err)
	}
	// Dedicated MCP identity token (aud aep-api-mcp): the runner bearer above
	// (aud git-service) is pinned-rejected by the MCP verifier, so the pod needs
	// a separate token to call the BFF's internal MCP surface (list endpoints /
	// read remote file / search remote code). One token stamped at dispatch,
	// TTL matching the runner bearer's 24h Job lifetime — no refresh route.
	mcpToken, err := e.tokens.IssueServiceToken(auth.AudienceMCP, in.orgID, 24*time.Hour)
	if err != nil {
		return "", fmt.Errorf("mint MCP token: %w", err)
	}
	// Resolve the org's skills repo URL (provisioning on first touch) so the
	// runner can clone it and resolve applied skills locally. Best-effort — ""
	// on any failure, the runner then degrades to the base plugin.
	skillsRepoURL := e.resolveSkillsRepoURL(ctx, in.orgID)

	// Proxy path (cloud / local-proxy plane): per-org NS + per-run ExternalSecrets
	// + K8s Job via the cluster-gateway-proxy. Falls back to the direct K8s Job
	// path below when the proxy / SM-API is not configured.
	used, runName, perr := e.dispatchViaProxy(ctx, in, repo, name, email, login, bearer, mcpToken, skillsRepoURL)
	if perr != nil {
		return "", perr
	}
	if used {
		return runName, nil
	}

	// Validation has no non-proxy fallback. The image is no longer the reason
	// (one image serves both kinds); K8sJobInput is: it carries no TaskKind and
	// no deadline override, so the direct path would launch a runner that never
	// preloads the `aep-validation` skill and dies at the 1h default. Fail
	// loudly rather than launch a run that cannot do the job.
	if in.shape.taskKind != "" || in.shape.deadline != 0 {
		return "", fmt.Errorf("dispatch kind %q requires the cluster-gateway-proxy path; the direct K8s Job fallback carries no AEP_TASK_KIND or deadline override", in.shape.taskKind)
	}

	// Direct K8s Job path: creates the org's data-plane namespace, SA, Anthropic
	// secret, and Job directly via the in-cluster client. No cluster-gateway-proxy
	// or SM-API needed — the sole fallback for aep-init local installs.
	if e.k8sJob != nil {
		orgUUID, uuidErr := e.lookupOrgUUID(ctx, in.orgID)
		if uuidErr != nil {
			return "", fmt.Errorf("k8s-job dispatch: lookup org UUID for %q: %w", in.orgID, uuidErr)
		}
		rn, k8serr := e.k8sJob.Dispatch(ctx, K8sJobInput{
			RunName:       codingAgentRunNameFor(in.correlationID),
			OrgID:         in.orgID,
			OrgUUID:       orgUUID,
			ProjectID:     in.projectID,
			Component:     in.shape.componentName,
			ExecutionID:   in.correlationID,
			RepoURL:       repo.RepoURL,
			Prompt:        in.shape.prompt,
			IdentityName:  name,
			IdentityEmail: email,
			IdentityLogin: login,
			Bearer:        bearer,
			SkillsRepoURL: skillsRepoURL,
		})
		if k8serr != nil {
			return "", k8serr
		}
		return rn, nil
	}

	return "", fmt.Errorf("no coding-agent dispatch path configured: set CLUSTER_GATEWAY_PROXY_URL or ensure in-cluster client + AGENT_RUNNER_IMAGE + AGENT_PLATFORM_URL are set")
}

// dispatchViaProxy runs the cluster-gateway-proxy apply-chain for one agent
// launch — the same recipe as the legacy dispatch service's tryDispatchViaProxy.
// used=false ⇒ not configured for the proxy path (fall back). The runner env
// AEP_TASK_ID carries the launch's correlation id (JobInputs.TaskID) and so does
// the bearer's task claim — the re-keyed runner contract (§9.2).
func (e *CodingExecutor) dispatchViaProxy(ctx context.Context, in agentLaunch, repo *sourcecontrol.GitRepository, name, email, login, bearer string, mcpToken, skillsRepoURL string) (bool, string, error) {
	disp := in.shape
	if e.proxy == nil || e.runnerImage == "" || e.clusterSecretStore == "" {
		return false, "", nil
	}
	anthropicRow, err := e.anthropicCreds.GetByOrg(ctx, in.orgID)
	if err != nil || anthropicRow == nil {
		slog.InfoContext(ctx, "proxy dispatch: anthropic row missing; falling back", "org", in.orgID, "error", err)
		return false, "", nil
	}
	githubRow, err := e.githubCreds.GetByOrg(ctx, in.orgID)
	if err != nil || githubRow == nil {
		slog.InfoContext(ctx, "proxy dispatch: github row missing; falling back", "org", in.orgID, "error", err)
		return false, "", nil
	}
	if anthropicRow.SMAPIKVPath == nil || githubRow.SMAPIKVPath == nil {
		slog.InfoContext(ctx, "proxy dispatch: SM-API triplet missing; falling back", "org", in.orgID)
		return false, "", nil
	}

	// Publisher cc (cloud gateway only). Best-effort provision + SM-API triplet.
	var (
		publisherSR       *SecretRef
		publisherTokenURL string
	)
	if e.idp != nil {
		if _, _, _, perr := e.idp.EnsureOrgPublisher(ctx, in.orgID, "dispatch"); perr != nil {
			slog.ErrorContext(ctx, "proxy dispatch: EnsureOrgPublisher failed — runner cc may be invalid", "org", in.orgID, "error", perr)
		}
	}
	if idpRow, err := e.idpProfiles.GetProfileByOrgID(ctx, in.orgID); err == nil && idpRow != nil {
		if idpRow.SMAPIKVPath != nil && idpRow.SMAPISecretRefName != nil {
			publisherSR = &SecretRef{
				SecretRefName: derefStr(idpRow.SMAPISecretRefName),
				KVPath:        derefStr(idpRow.SMAPIKVPath),
				Property:      derefStr(idpRow.SMAPIProperty),
			}
			publisherTokenURL = deriveTokenURLFromJWKS(idpRow.JWKSURL)
			if publisherTokenURL == "" {
				publisherSR = nil
			}
		}
	}
	// On the cloud gateway (https) a per-task JWT is rejected — publisher cc is
	// mandatory. Local k3d (http) keeps the bearer fallback.
	if publisherSR == nil && isGatewayPlatformURL(e.platformURL) {
		return false, "", fmt.Errorf("publisher cc not provisioned for org %q: the coding-agent runner cannot authenticate through the gateway", in.orgID)
	}

	orgUUID, err := e.lookupOrgUUID(ctx, in.orgID)
	if err != nil {
		slog.InfoContext(ctx, "proxy dispatch: org UUID not found; falling back", "org", in.orgID, "error", err)
		return false, "", nil
	}
	runName := codingAgentRunNameFor(in.correlationID)
	job := JobInputs{
		RunName: runName,
		// NAMING DEBT: JobInputs.TaskID → the Job's AEP_TASK_ID env carries the
		// launch's CORRELATION id (an execution id on the funnel path, a cycle id
		// on the milestone-run path), never a Task id. Un-renamed to avoid a
		// cluster-workflow + runner rename.
		TaskID:                in.correlationID,
		OrgID:                 in.orgID,
		ProjectID:             in.projectID,
		ComponentName:         disp.componentName,
		RunnerImage:           e.runnerImage,
		TaskKind:              disp.taskKind,
		ActiveDeadlineSeconds: disp.deadline,
		RepoURL:               repo.RepoURL,
		Prompt:                disp.prompt,
		IdentityName:          name,
		IdentityEmail:         email,
		IdentityLogin:         login,
		GitServiceURL:         e.gitServiceURL,
		CallbackURL:           e.platformURL,
		Bearer:                bearer,
		MCPToken:              mcpToken,
		SkillsRepoURL:         skillsRepoURL,
		PublisherTokenURL:     publisherTokenURL,
	}
	// Resolve the component's external-resource secret bundles so the dispatcher
	// materialises each into a per-run ExternalSecret the runner mounts (the agent
	// integration-tests against the live service). Best-effort: on failure the run
	// dispatches without them (identical to no secret-bearing external deps).
	// Component launches only — a validation task and a milestone cycle are
	// project-scoped (no single component, no component-bound external resources).
	var extResSRs []ExternalResourceSecretInputs
	if e.runnerSecrets != nil && in.secretComponent != "" {
		if srs, rerr := e.runnerSecrets.ResolveRunnerSecrets(ctx, in.orgID, in.projectID, in.secretComponent, openchoreo.DevEnvironmentName); rerr != nil {
			slog.WarnContext(ctx, "coding executor: resolve external-resource runner secrets failed — dispatching without", "component", in.secretComponent, "error", rerr)
		} else {
			extResSRs = srs
		}
	}
	rn, err := e.proxy.Dispatch(ctx, Inputs{
		OrgUUID:                orgUUID,
		Job:                    job,
		AnthropicSR:            SecretRef{SecretRefName: derefStr(anthropicRow.SMAPISecretRefName), KVPath: derefStr(anthropicRow.SMAPIKVPath), Property: derefStr(anthropicRow.SMAPIProperty)},
		GitHubSR:               SecretRef{SecretRefName: derefStr(githubRow.SMAPISecretRefName), KVPath: derefStr(githubRow.SMAPIKVPath), Property: derefStr(githubRow.SMAPIProperty)},
		PublisherSR:            publisherSR,
		ExternalResourceSRs:    extResSRs,
		ClusterSecretStoreName: e.clusterSecretStore,
	})
	if err != nil {
		return false, "", err
	}
	return true, rn, nil
}

// stageBuildSecret pre-stages the org's build git credential and returns the
// secretRef to pass to the build WorkflowRun (its checkout-source step clones
// the project repo). Mirrors feature/component's TriggerBuild staging: no stager
// wired or no repo slug → clone unauthenticated (empty secretRef, correct for
// public repos); an ownership/disconnect/transient staging error blocks the
// retry. The local plane sets GITHUB_REPO_VISIBILITY=private, so this is what
// makes project builds clone at all.
func (e *CodingExecutor) stageBuildSecret(ctx context.Context, orgID, projectID, runName string) (string, error) {
	if e.buildSecrets == nil {
		return "", nil
	}
	repo, err := e.repos.GetRepo(ctx, orgID, projectID)
	if err != nil {
		slog.WarnContext(ctx, "build: resolve repo for secret staging failed — cloning unauthenticated", "org", orgID, "project", projectID, "error", err)
		return "", nil
	}
	if repo == nil || repo.RepoSlug == "" {
		slog.WarnContext(ctx, "build: no repo slug — cloning unauthenticated", "org", orgID, "project", projectID)
		return "", nil
	}
	secretRef, err := e.buildSecrets.StageBuildSecret(ctx, orgID, repo.RepoSlug, runName)
	if err != nil {
		return "", fmt.Errorf("stage build secret: %w", err)
	}
	return secretRef, nil
}

// RetryAuthFailedBuild re-mints the build clone credential and re-triggers the
// build at the row's pinned CommitSHA under a fresh run name, returning that
// name for the caller (ExecWatcher) to thread onto the row. It is the
// git-clone-auth retry the legacy build watcher's RetryAuthFailedBuild provided,
// re-keyed to the execution row. A staging refusal (org disconnected / repo not
// in org) aborts the retry — the watcher exhausts the budget instead.
func (e *CodingExecutor) RetryAuthFailedBuild(ctx context.Context, row *delivery.Execution) (string, error) {
	if row == nil {
		return "", fmt.Errorf("retry-auth-failed: nil execution")
	}
	if row.CommitSHA == "" {
		return "", fmt.Errorf("retry-auth-failed: execution %s has no commit SHA", row.ID)
	}
	if row.Component == "" {
		return "", fmt.Errorf("retry-auth-failed: execution %s has no component", row.ID)
	}
	runName := openchoreo.NewBuildRunName(row.ProjectID, row.Component)
	secretRef, err := e.stageBuildSecret(ctx, row.OrgID, row.ProjectID, runName)
	if err != nil {
		return "", err
	}
	run, err := e.oc.TriggerBuildAtCommit(ctx, row.OrgID, row.ProjectID, row.Component, row.CommitSHA, secretRef, runName)
	if err != nil {
		return "", fmt.Errorf("retry-auth-failed: trigger build: %w", err)
	}
	return run.Name, nil
}

func (e *CodingExecutor) lookupOrgUUID(ctx context.Context, ocOrgID string) (string, error) {
	org, err := e.orgs.GetByName(ctx, ocOrgID)
	if err != nil {
		return "", err
	}
	if org == nil {
		return "", fmt.Errorf("organization %s not found", ocOrgID)
	}
	if org.ThunderOrgUUID != nil && *org.ThunderOrgUUID != uuid.Nil {
		return org.ThunderOrgUUID.String(), nil
	}
	if org.UUID == uuid.Nil {
		return "", fmt.Errorf("organization %s has no UUID", ocOrgID)
	}
	return org.UUID.String(), nil
}

// proxyJobRunPrefix marks a run name as a cluster-gateway-proxy coding-agent Job
// (a K8s Job owned by the JobWatcher) rather than an OpenChoreo WorkflowRun. It
// is the ONE discriminator both watchers key on so they never poll each other's
// runs: the JobWatcher processes ONLY these, the ExecWatcher skips them.
const proxyJobRunPrefix = "ca-"

// isProxyJobRun reports whether runName is a proxy-dispatched coding-agent Job
// (vs an OpenChoreo WorkflowRun). Shared by the ExecWatcher (skips) and the
// JobWatcher (claims).
func isProxyJobRun(runName string) bool {
	return strings.HasPrefix(runName, proxyJobRunPrefix)
}

// codingAgentRunNameFor derives a deterministic `ca-…` run name from an id + a
// UTC minute bucket (same shape as the legacy codingAgentRunName; a re-dispatch
// within the minute reuses the name and the immutable Job is DELETE+POST'd).
func codingAgentRunNameFor(id string) string {
	minute := time.Now().UTC().Format("0601021504")
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	name := fmt.Sprintf("%s%s-%s", proxyJobRunPrefix, shortID, minute)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// isGatewayPlatformURL reports whether the runner reaches the BFF through the
// IdP-authed cloud gateway (https) rather than an internal http URL (local k3d).
func isGatewayPlatformURL(u string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "https://")
}

// deriveTokenURLFromJWKS swaps a trailing `/oauth2/jwks` for `/oauth2/token`
// (same Thunder host). Returns "" on unrecognized shapes.
func deriveTokenURLFromJWKS(jwksURL string) string {
	const suffix = "/oauth2/jwks"
	j := strings.TrimSpace(jwksURL)
	if !strings.HasSuffix(j, suffix) {
		return ""
	}
	return strings.TrimSuffix(j, suffix) + "/oauth2/token"
}

// buildPrompt is the coding-agent directive (§9): a MILESTONE REFERENCE and
// nothing else. The agent discovers its own working set from the live issues
// API and follows the versioned `aep` skill for ordering, fan-out, branch
// identity, verification and the PR contract — the platform deliberately
// carries no procedure in the prompt, so the workflow versions with the skill
// rather than with the BFF binary.
func buildPrompt(milestoneNumber int, milestoneTitle string) string {
	return fmt.Sprintf("Work the issues for milestone %d (%q). Follow the `aep` skill loaded in your session — it defines discovery, ordering, fan-out, branch identity, verification, the PR contract and the deny-list.", milestoneNumber, milestoneTitle)
}

// validationComponentSentinel is the AEP_COMPONENT_NAME a validation Job carries.
// A validation Task is project-scoped (no component); this is a valid k8s label
// value the Job/pod is stamped with.
const validationComponentSentinel = "aep-validation"

// validationTaskKind is the runner's AEP_TASK_KIND for a validation cycle: it
// is what makes the runner preload the `aep-validation` skill instead of `aep`.
const validationTaskKind = "validation"

// validationDeadlineSeconds bounds a validation run (2h): browser boot + live
// exploration + authoring/healing e2e specs is longer than a coding run.
const validationDeadlineSeconds int64 = 7200

// dispatchShape carries the per-class knobs runCoding hands to the proxy
// dispatch: the coding class and the validation class share the executor, the
// proxy plumbing AND the runner image, differing only in these values.
type dispatchShape struct {
	prompt        string
	componentName string
	taskKind      string // "" (coding) | "validation"
	deadline      int64  // 0 → job_template's 1h default
}

// buildValidationPrompt is the validation-runner directive: it points at the
// validation issue and defers the workflow to the aep-validation skill (the
// runner preloads it because AEP_TASK_KIND=validation).
func buildValidationPrompt(issueURL string, issueNumber int) string {
	return fmt.Sprintf("This is a validation task. Work on this GitHub validation issue: %s\n\nFollow the `aep-validation` skill's workflow: fetch the validation context, author and run the e2e tests against the deployed system, commit the tests and report, and open a PR whose body includes `Closes #%d` so the platform links it back.", issueURL, issueNumber)
}
