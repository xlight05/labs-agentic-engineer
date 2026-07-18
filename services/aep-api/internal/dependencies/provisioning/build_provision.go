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

package provisioning

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/models"
)

// Input kinds the build drawer emits (issue #164). These are the drawer input
// kinds carried on ProvisionInput.Kind — NOT the design dependency kinds (an
// external dependency's config-collection input is "external-config").
const (
	buildKindExternalConfig = "external-config"
	buildKindPlatformResrc  = "platform-resource"
	buildKindOrgService     = "org-service"
)

// BuildProvisionInput is one dependency's resolved provisioning payload for the
// build path — field-identical to devflow.ProvisionInput but a provisioning
// package type (this feature must not import the workflow orchestrator; the
// app-root adapter maps devflow.ProvisionInput ⇄ BuildProvisionInput). A raw
// secret value is NEVER carried here — SecretRefByEnv holds the SM-API reference
// per env (staged pre-tag by POST /build).
type BuildProvisionInput struct {
	Component      string
	Dependency     string
	Kind           string
	Config         map[string]string
	SecretRefByEnv map[string]string
	Parameters     map[string]any
	Approved       bool
}

// ProvisionFailure is one dependency's provisioning failure — data the workflow
// inspects (not an activity error). It carries no secret values.
type ProvisionFailure struct {
	Component  string
	Dependency string
	Reason     string
}

// ProvisionForBuild authors the project's dependencies from the build drawer
// inputs the dev workflow carries (issue #164). It mints the aep:provision gate
// issues ONCE, then authors each input BY KIND:
//   - external-config: the SaveValues-style synchronous flow using the already
//     staged secret reference (AuthorWithSecretRef — no SM-API write), closing
//     the gate synchronously.
//   - platform-resource: the async Provision path (the readiness watcher closes
//     the gate out-of-band).
//   - org-service: a no-op here (Task 4 fills it).
//
// A returned error means "retry the activity" (the gate mint failed). A per-input
// author failure is appended to the returned failures and the batch CONTINUES —
// the workflow decides what to do with them (it fails the run). orgID is the OC
// namespace + issues org; ocOrgID is the SM-API org id (reserved for the org
// path; the external author half needs no SM write). The external deps must
// already be registered in the catalog (the app-root adapter calls
// design.RegisterExternalResources before this — Task 5).
func (s *Service) ProvisionForBuild(ctx context.Context, orgID, ocOrgID, projectID, tag string, inputs []BuildProvisionInput) ([]ProvisionFailure, error) {
	// Mint gates only when the drawer carried inputs. A not-ready dependency is
	// always surfaced in the build drawer, so a build with no inputs needs no new
	// gate — and a pure re-build must not churn a fresh gate for every already-ready
	// dep. Existing gates are still reconciled by settleReadyGates below, so an
	// orphaned gate self-heals on ANY later build, drawer or not (issue #164).
	// gateByDep carries the aep:provision gate issue number the mint step KNOWS for
	// each dep — captured from the CreateIssue result, not re-looked-up via GitHub's
	// eventually-consistent label list (which lags a just-created gate and strands
	// the provision run — issue #164). Empty when no inputs were carried; a missing
	// dep resolves to 0 (a safe no-op gate).
	var gateByDep map[string]int
	if len(inputs) > 0 {
		var err error
		gateByDep, err = s.EnsureProvisionIssues(ctx, orgID, projectID, tag)
		if err != nil {
			return nil, err
		}
	}

	var failures []ProvisionFailure
	provisioned := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		provisioned[strings.ToLower(in.Dependency)] = true
	}
	for _, in := range inputs {
		gate := gateByDep[strings.ToLower(in.Dependency)]
		switch in.Kind {
		case buildKindExternalConfig:
			if err := s.authorExternalWithRef(ctx, orgID, ocOrgID, projectID, in, gate); err != nil {
				failures = append(failures, ProvisionFailure{Component: in.Component, Dependency: in.Dependency, Reason: err.Error()})
			}
		case buildKindPlatformResrc:
			if err := s.provisionResource(ctx, orgID, projectID, in.Dependency, gate, in.Parameters, nil); err != nil {
				failures = append(failures, ProvisionFailure{Component: in.Component, Dependency: in.Dependency, Reason: err.Error()})
			}
		case buildKindOrgService:
			// Cross-project org-service visibility (issue #164, Task 4): for an
			// APPROVED-but-unresolved org-service dep, record the access request, mint
			// the consumer-side visibility gate, and kick the provider's build. An
			// unapproved dep is left alone (the user did not opt in). Per-input batch
			// semantics: a failure becomes a ProvisionFailure and the batch continues.
			if in.Approved {
				if err := s.StartOrgServiceVisibility(ctx, orgID, projectID, in.Dependency); err != nil {
					failures = append(failures, ProvisionFailure{Component: in.Component, Dependency: in.Dependency, Reason: err.Error()})
				}
			}
		default:
			slog.WarnContext(ctx, "provisioning: unknown build provision kind", "kind", in.Kind, "dependency", in.Dependency)
		}
	}

	// Reconcile every provision gate whose dep is NOT in inputs. EnsureProvisionIssues
	// re-mints an OPEN gate for each external + platform-resource dep in the design,
	// but the inputs loop only admits a run for the drawer subset. A dep whose OC
	// binding is already Ready is deliberately skipped by build preflight, so it
	// lands here with a fresh gate and no run — deriving `pending` forever and
	// stranding every consumer coding task (issue #164). Settle it: admit+complete a
	// provision run so its gate derives `deployed`, without re-authoring the resource.
	failures = append(failures, s.settleReadyGates(ctx, orgID, projectID, provisioned)...)
	return failures, nil
}

// settleReadyGates reconciles provision gates for deps that are already Ready in
// OpenChoreo but are NOT in the build drawer inputs (issue #164). For each such
// dep it admits+completes a provision run so the freshly-minted gate derives
// `deployed` instead of stranding consumers on a run-less `pending` gate. It
// re-authors NOTHING — the resource is already Ready. Best-effort: a design read
// hiccup must not fail the build (log + return nil); a per-dep settle error
// becomes a ProvisionFailure the workflow can inspect.
func (s *Service) settleReadyGates(ctx context.Context, orgID, projectID string, provisioned map[string]bool) []ProvisionFailure {
	comps, err := s.design.ReadDesignComponents(ctx, orgID, projectID)
	if err != nil {
		slog.WarnContext(ctx, "provisioning: settle read design failed", "error", err)
		return nil
	}
	var failures []ProvisionFailure
	seen := map[string]bool{}
	for _, comp := range comps {
		for _, dep := range comp.Dependencies {
			// Only external + platform-resource deps mint a provision gate.
			if dep.Kind != models.DependencyKindExternal && dep.Kind != models.DependencyKindPlatformResource {
				continue
			}
			name := strings.ToLower(dep.Name)
			// A dep in inputs is provisioned by the inputs loop; dedupe multi-consumer deps.
			if provisioned[name] || seen[name] {
				continue
			}
			seen[name] = true
			// Only an already-Ready dep is settled here. A not-Ready dep is driven by
			// its own drawer input (or is genuinely un-actionable) — leave it alone.
			st, serr := s.Status(ctx, orgID, projectID, dep.Name, "")
			if serr != nil || st == nil || !st.Ready {
				continue
			}
			if cerr := s.completeReadyGate(ctx, orgID, projectID, dep.Name, comp.Name); cerr != nil {
				failures = append(failures, ProvisionFailure{Component: comp.Name, Dependency: dep.Name, Reason: cerr.Error()})
			}
		}
	}
	return failures
}

// completeReadyGate admits and completes a provision run for an already-Ready
// dep's open gate so it derives `deployed` (issue #164). It mirrors the
// admit→StartWithRun→completeProvisionRow TAIL of authorExternalWithRef but
// authors NOTHING — the OC binding is already Ready, so re-authoring would only
// re-write state. No open gate (issueNumber == 0) or an already-active run
// (!admitted) is an idempotent no-op.
func (s *Service) completeReadyGate(ctx context.Context, orgID, projectID, depName, component string) error {
	slog.DebugContext(ctx, "provisioning: settling already-ready gate", "dependency", depName, "component", component)
	issueNumber, _, err := s.findProvisionIssue(ctx, orgID, projectID, depName)
	if err != nil {
		return err
	}
	if issueNumber == 0 {
		return nil
	}
	repo, err := s.repos.RepoFullName(ctx, orgID, projectID)
	if err != nil {
		return fmt.Errorf("provisioning: resolve repo: %w", err)
	}
	row, admitted, err := s.admitProvisionRow(ctx, orgID, projectID, repo, depName, issueNumber)
	if err != nil {
		return fmt.Errorf("provisioning: admit provision run: %w", err)
	}
	if !admitted {
		// A provision run is already active for this gate (e.g. a concurrent settle).
		return nil
	}
	ref := dependencies.ExternalResourceBindingName(projectID, depName, defaultEnv)
	if _, serr := s.execs.StartWithRun(ctx, row.ID, ref); serr != nil {
		slog.WarnContext(ctx, "provisioning: start settle provision run failed", "execution", row.ID, "error", serr)
	}
	s.completeProvisionRow(ctx, orgID, projectID, issueNumber, row.ID,
		fmt.Sprintf("Dependency `%s` already provisioned (OC binding Ready) — gate settled.", depName))
	return nil
}

// authorExternalWithRef runs the synchronous external-config provisioning flow
// from an already-staged secret reference — mirrors SaveValues (admit gate row →
// author OC binding → complete gate row) but authors via AuthorWithSecretRef
// (the plain config + the staged secretStorePath per env) so no secret value is
// re-written to SM-API.
func (s *Service) authorExternalWithRef(ctx context.Context, orgID, ocOrgID, projectID string, in BuildProvisionInput, gateNumber int) error {
	_ = ocOrgID // the author half needs no SM-API write; kept for symmetry with SaveValues.
	if _, err := s.findDepInProject(ctx, orgID, projectID, in.Dependency, models.DependencyKindExternal); err != nil {
		return err
	}
	er, err := s.catalog.Get(ctx, orgID, in.Dependency)
	if err != nil {
		return fmt.Errorf("provisioning: get external resource %q: %w", in.Dependency, err)
	}
	if er == nil {
		return dependencies.ErrNotRegistered
	}
	byEnv := preparedEnvValues(in)

	// Admit a provision run for the gate issue (when one exists). The build path
	// threads the KNOWN gate number (from the mint's CreateIssue result) so we skip
	// GitHub's eventually-consistent label list, which often lags a just-created
	// gate (issue #164). Fall back to the list lookup only when no number is known
	// (defends any caller without one).
	issueNumber := gateNumber
	if issueNumber == 0 {
		if issueNumber, _, err = s.findProvisionIssue(ctx, orgID, projectID, in.Dependency); err != nil {
			return err
		}
	}
	var execID string
	if issueNumber > 0 {
		repo, rerr := s.repos.RepoFullName(ctx, orgID, projectID)
		if rerr != nil {
			return fmt.Errorf("provisioning: resolve repo: %w", rerr)
		}
		row, admitted, aerr := s.admitProvisionRow(ctx, orgID, projectID, repo, in.Dependency, issueNumber)
		if aerr != nil {
			return fmt.Errorf("provisioning: admit provision run: %w", aerr)
		}
		if admitted {
			execID = row.ID
		}
	}

	result, perr := s.extProv.AuthorWithSecretRef(ctx, orgID, projectID, er, byEnv)
	if perr != nil {
		if execID != "" {
			s.failProvisionRow(ctx, orgID, projectID, issueNumber, execID, perr.Error())
		}
		return fmt.Errorf("%w: %v", dependencies.ErrProvisionFailed, perr)
	}

	if execID != "" {
		ref := result.BindingByEnv[defaultEnv]
		if ref == "" {
			ref = result.ResourceName
		}
		if _, serr := s.execs.StartWithRun(ctx, execID, ref); serr != nil {
			slog.WarnContext(ctx, "provisioning: start external provision run failed", "execution", execID, "error", serr)
		}
		s.completeProvisionRow(ctx, orgID, projectID, issueNumber, execID,
			fmt.Sprintf("External resource `%s` configured (OC binding `%s`).", in.Dependency, ref))
	}
	return nil
}

// preparedEnvValues turns a build external-config input into the per-env author
// inputs: the non-secret Config (applied to every env) + the staged
// secretStorePath per env. When no secret was staged the input still authors the
// default env's binding from the plain config alone.
func preparedEnvValues(in BuildProvisionInput) map[string]dependencies.PreparedEnvValues {
	byEnv := map[string]dependencies.PreparedEnvValues{}
	for env, ref := range in.SecretRefByEnv {
		byEnv[env] = dependencies.PreparedEnvValues{Plain: in.Config, SecretStorePath: ref}
	}
	if len(byEnv) == 0 {
		byEnv[defaultEnv] = dependencies.PreparedEnvValues{Plain: in.Config}
	}
	return byEnv
}
