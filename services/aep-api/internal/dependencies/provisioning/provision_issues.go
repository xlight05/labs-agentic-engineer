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

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// provisionDep is one distinct provisioning dependency discovered in a design.
type provisionDep struct {
	name         string
	gateKind     string // config-collection | resource-provisioning
	resourceType string // platform-resource only
}

// EnsureProvisionIssues mints one aep:provision gate issue per distinct external
// / platform-resource dependency in the project's approved design, deduped per
// project (an open gate issue for a dependency name is never re-created). It is
// idempotent and safe to call after every plan (dependency-management §3.6 step
// 4: "Planning mints coding issues AND provisioning issues"). The gate issues
// hold their consumer coding tasks until each derives deployed. Best-effort per
// issue: a single create failure is logged and does not abort the rest.
func (s *Service) EnsureProvisionIssues(ctx context.Context, orgID, projectID, designTag string) (map[string]int, error) {
	comps, err := s.design.ReadDesignComponents(ctx, orgID, projectID)
	if err != nil {
		return nil, fmt.Errorf("provisioning: read design: %w", err)
	}
	distinct := distinctProvisionDeps(comps)
	if len(distinct) == 0 {
		return nil, nil
	}

	existing, err := s.openProvisionDeps(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	// gateByDep maps a lowercased dep name → its OPEN aep:provision gate issue
	// number, for both pre-existing gates and the ones minted below. The build path
	// threads these numbers directly into provisioning (read-your-write from the
	// CreateIssue result) instead of re-looking them up via GitHub's
	// eventually-consistent label-filtered list, which often lags a just-created
	// gate (issue #164).
	gateByDep := make(map[string]int, len(distinct))
	for key, num := range existing {
		gateByDep[key] = num
	}

	var created int
	for key, dep := range distinct {
		if existing[key] > 0 {
			continue
		}
		title := provisionIssueTitle(dep)
		block := taskmeta.Block{
			Component: dep.name,
			GateKind:  dep.gateKind,
			Origin:    taskmeta.OriginSpecPlan,
			// SpecTag mirrors DesignTag: the planner stamps a coding task's SpecTag
			// and DesignTag to the SAME version tag (plan.go), so a gate carrying the
			// same tag lets the tag-scoped Builds view (which filters on SpecTag) show
			// its provision/config gates alongside the coding tasks they gate — and
			// lets the on_hold reconciler see the gate for that build (issue #164).
			SpecTag:   designTag,
			DesignTag: designTag,
			Key:       taskmeta.Key(projectID, designTag, dep.name, title),
		}
		body := taskmeta.ComposeBody(block, taskmeta.Human{
			Rationale: provisionIssueRationale(dep),
			Body:      provisionIssueScope(dep),
		})
		req := sourcecontrol.CreateIssueRequest{
			Title:  title,
			Body:   body,
			Labels: taskmeta.NewTaskLabels(taskmeta.ClassProvision, taskmeta.OriginSpecPlan),
		}
		res, cerr := s.issues.CreateIssue(ctx, orgID, projectID, req)
		if cerr != nil {
			slog.WarnContext(ctx, "provisioning: create gate issue failed", "dep", dep.name, "error", cerr)
			continue
		}
		// Capture the minted number from the CREATE result — read-your-write, no list.
		if res != nil {
			gateByDep[key] = res.Number
		}
		created++
	}
	if created > 0 {
		slog.InfoContext(ctx, "provisioning: minted gate issues", "project", projectID, "count", created)
	}
	return gateByDep, nil
}

// distinctProvisionDeps collects the project's distinct external +
// platform-resource dependencies (keyed by lowercased name — the same dependency
// consumed by several components is one gate issue).
func distinctProvisionDeps(comps []spec.DesignComponent) map[string]provisionDep {
	out := map[string]provisionDep{}
	for i := range comps {
		for j := range comps[i].Dependencies {
			d := comps[i].Dependencies[j]
			key := strings.ToLower(d.Name)
			if key == "" {
				continue
			}
			if _, seen := out[key]; seen {
				continue
			}
			switch d.Kind {
			case spec.DependencyKindExternal:
				out[key] = provisionDep{name: d.Name, gateKind: taskmeta.GateConfigCollection}
			case spec.DependencyKindPlatformResource:
				out[key] = provisionDep{name: d.Name, gateKind: taskmeta.GateResourceProvisioning, resourceType: d.ResourceType}
			}
		}
	}
	return out
}

// openProvisionDeps returns dependency names (lowercased) that already have an
// open aep:provision gate issue, mapped to that gate's issue number. Only
// pre-existing (listable) gates are returned — a JUST-created gate races GitHub's
// eventually-consistent list, so the build path captures those numbers from the
// CreateIssue result instead (issue #164).
func (s *Service) openProvisionDeps(ctx context.Context, orgID, projectID string) (map[string]int, error) {
	issues, err := s.issues.ListIssues(ctx, orgID, projectID, []string{taskmeta.LabelMarker})
	if err != nil {
		return nil, fmt.Errorf("provisioning: list issues: %w", err)
	}
	out := map[string]int{}
	for _, issue := range issues {
		if !strings.EqualFold(issue.State, "open") {
			continue
		}
		if taskmeta.ParseLabels(issue.Labels).Class != taskmeta.ClassProvision {
			continue
		}
		block, berr := taskmeta.ParseBlock(issue.Body)
		if berr != nil {
			continue
		}
		if block.Component != "" {
			out[strings.ToLower(block.Component)] = issue.Number
		}
	}
	return out, nil
}

func provisionIssueTitle(dep provisionDep) string {
	if dep.gateKind == taskmeta.GateResourceProvisioning {
		if dep.resourceType != "" {
			return fmt.Sprintf("Provision resource: %s (%s)", dep.name, dep.resourceType)
		}
		return "Provision resource: " + dep.name
	}
	return "Provide configuration: " + dep.name
}

func provisionIssueRationale(dep provisionDep) string {
	if dep.gateKind == taskmeta.GateResourceProvisioning {
		return "A platform resource this project depends on must be provisioned before dependent components can deploy."
	}
	return "An external dependency this project consumes needs its configuration/secret values before dependent components can deploy."
}

func provisionIssueScope(dep provisionDep) string {
	if dep.gateKind == taskmeta.GateResourceProvisioning {
		return fmt.Sprintf("## Provision `%s`\n\nConfirm the provisioning parameters for this platform resource in the "+
			"architecture drawer. The platform provisions it and closes this issue once the resource is ready — "+
			"no manual action on this issue is needed.", dep.name)
	}
	return fmt.Sprintf("## Configure `%s`\n\nProvide this dependency's configuration/secret values in the architecture "+
		"drawer. Secret values are stored in the secret manager and never appear here. The platform closes this issue "+
		"once the values are collected.", dep.name)
}
