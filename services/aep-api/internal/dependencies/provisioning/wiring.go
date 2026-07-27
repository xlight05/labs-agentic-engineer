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

// DECLARATIVE DEPENDENCY WIRING (ADR-0004).
//
// The coding agent authors every line of a component's workload.yaml, the
// platform never patches a deployed Workload CR — so the platform's only way to
// tell the agent where a dependency lives is to SAY so, on an issue the agent
// reads. This file resolves a project's dependency targets into the
// `dependencies:` block the agent copies verbatim, and posts it as the
// "Platform-resolved dependencies" comment.
//
// WHEN it posts is the whole design: at GATE RESOLUTION. A dependency's address
// does not exist until its OC binding is Ready, and that is the moment
// completeProvisionRow runs — the same moment the gate closes and dispatch is
// re-evaluated. Earlier (plan time) there is nothing to say; later (the
// merged-PR fan-out) the agent has already written the file.
//
// WHO it posts to is the run's WORKING SET: the project's open `aep` issues,
// minus the gates and the validation issue. A dependency is project-level and
// nothing platform-side attributes an ISSUE to a component — bodies are prose
// and a title is renamable — so the recipient set is the whole working set and
// the CONTENT is keyed by component instead: one block per design component that
// consumes the dependency, each naming the workload.yaml it belongs in. One
// agent works the whole milestone in a cycle, so that reaches the reader either
// way; the cost is a comment on a sibling issue whose component has no block.
//
// Posting is IDEMPOTENT on the aep:wired/<slug> label (gate_labels.go): an issue
// already carrying it is skipped, so a re-settled gate does not pile comments up.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// workloadDeps is the flat OC WorkloadDescriptor `dependencies:` block, rendered
// to the YAML the coding agent merges into its component's workload.yaml.
// Sub-keys are emitted only when non-empty (omitempty).
type workloadDeps struct {
	Endpoints []workloadEndpointDepYAML `yaml:"endpoints,omitempty"`
	Resources []workloadResourceDepYAML `yaml:"resources,omitempty"`
}

type workloadEndpointDepYAML struct {
	Project     string            `yaml:"project,omitempty"` // omit if same project
	Component   string            `yaml:"component"`
	Name        string            `yaml:"name"`
	Visibility  string            `yaml:"visibility"`
	EnvBindings map[string]string `yaml:"envBindings"` // {address: <ENV>}
}

type workloadResourceDepYAML struct {
	Ref         string            `yaml:"ref"`
	EnvBindings map[string]string `yaml:"envBindings"` // {<output>: <ENV>}
}

// postResolvedWiring posts the ADR-0004 comment for a dependency that has just
// become resolvable, on every working-set issue that has not had it yet.
//
// Best-effort throughout: this runs on the gate-resolution path, and a GitHub
// hiccup here must not undo a provision that succeeded. Every failure is logged
// and the next issue is still attempted.
func (s *Service) postResolvedWiring(ctx context.Context, orgID, projectID, depName string) {
	marker := wiredDepLabel(depName)
	if s.issues == nil || s.design == nil || marker == "" {
		return
	}
	// The audience first: it is one list call, and a gate that resolves while
	// nothing is open to work has nobody to tell.
	targets, err := s.openWorkingSet(ctx, orgID, projectID)
	if err != nil {
		slog.WarnContext(ctx, "provisioning: list working set for wiring comment failed",
			"project", projectID, "dependency", depName, "error", err)
		return
	}
	pending := targets[:0:0]
	for _, issue := range targets {
		if !delivery.HasLabel(issue.Labels, marker) {
			pending = append(pending, issue)
		}
	}
	if len(pending) == 0 {
		return
	}

	comps, err := s.design.ReadDesignComponents(ctx, orgID, projectID)
	if err != nil {
		slog.WarnContext(ctx, "provisioning: read design for wiring comment failed",
			"project", projectID, "dependency", depName, "error", err)
		return
	}
	body := s.resolvedWiringComment(ctx, orgID, projectID, depName, consumersOf(comps, depName))
	if body == "" {
		return // nothing in the design consumes it, or nothing resolves yet
	}

	for _, issue := range pending {
		if cerr := s.issues.CommentIssue(ctx, orgID, projectID, issue.Number, body); cerr != nil {
			slog.WarnContext(ctx, "provisioning: post wiring comment failed",
				"issue", issue.Number, "dependency", depName, "error", cerr)
			continue // no marker: the next resolve retries this issue
		}
		if lerr := s.issues.AddLabels(ctx, orgID, projectID, issue.Number, []string{marker}); lerr != nil {
			// The comment landed but the marker did not, so a re-run may repeat it.
			// A duplicate comment is noise; a missing one is a CrashLoopBackOff.
			slog.WarnContext(ctx, "provisioning: stamp wiring marker failed — a re-run may repeat this comment",
				"issue", issue.Number, "dependency", depName, "error", lerr)
		}
	}
	slog.InfoContext(ctx, "provisioning: posted resolved dependency wiring",
		"project", projectID, "dependency", depName, "issues", len(pending))
}

// openWorkingSet is the run's working set: the project's OPEN `aep` issues minus
// the dispatch gates and the validation issue — exactly the population a coding
// cycle works.
//
// It is not milestone-scoped, and does not need to be: cutting a version closes
// the previous milestone's still-open issues, so the project's open `aep` issues
// ARE the current increment's. The `aep` label rides the host's AND-semantics
// ?labels= filter, and is re-checked here because a label filter is the server's
// promise, not this code's.
func (s *Service) openWorkingSet(ctx context.Context, orgID, projectID string) ([]sourcecontrol.IssueInfo, error) {
	issues, err := s.issues.ListIssues(ctx, orgID, projectID, []string{delivery.LabelAgentWork})
	if err != nil {
		return nil, fmt.Errorf("provisioning: list working set: %w", err)
	}
	out := make([]sourcecontrol.IssueInfo, 0, len(issues))
	for _, issue := range issues {
		if !strings.EqualFold(issue.State, "open") {
			continue
		}
		if !delivery.HasLabel(issue.Labels, delivery.LabelAgentWork) {
			continue
		}
		if delivery.HasLabel(issue.Labels, delivery.LabelProvisionGate) ||
			delivery.HasLabel(issue.Labels, delivery.LabelValidationWork) {
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

// consumersOf returns the design components that declare a dependency of this
// name, in design order. A dependency is project-level, so several components
// may consume one — and each needs its own block.
func consumersOf(comps []spec.DesignComponent, depName string) []spec.DesignComponent {
	var out []spec.DesignComponent
	for i := range comps {
		for j := range comps[i].Dependencies {
			if strings.EqualFold(comps[i].Dependencies[j].Name, depName) {
				out = append(out, comps[i])
				break
			}
		}
	}
	return out
}

// resolvedWiringComment renders the comment body: a preamble plus one section
// per consuming component whose wiring resolves to something. Returns "" when
// nothing resolves, so the caller posts nothing rather than an empty block.
//
// A component's section carries its WHOLE resolved dependency set, not just the
// dependency that triggered this post: workload.yaml wants one merged block, and
// re-posting the full set as each dependency lands means the last comment is
// always the complete answer.
func (s *Service) resolvedWiringComment(ctx context.Context, orgID, projectID, depName string, consumers []spec.DesignComponent) string {
	var sections []string
	for _, comp := range consumers {
		block, contracts, resourceNotes, err := s.resolveDependenciesYAML(ctx, orgID, projectID, comp)
		if err != nil {
			slog.WarnContext(ctx, "provisioning: resolve wiring for component failed",
				"project", projectID, "component", comp.Name, "error", err)
			continue
		}
		if block == "" {
			continue
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "## Component `%s`\n\nAdd this to `%s`'s `workload.yaml`:\n\n```yaml\n%s```",
			comp.Name, comp.Name, block)
		if contracts != "" {
			sb.WriteString("\n\n**Consumed API contracts** — before writing any client code, fetch the " +
				"exact contract for each provider below. Do not guess at request/response shapes or " +
				"endpoint paths:\n\n")
			sb.WriteString(contracts)
		}
		if resourceNotes != "" {
			sb.WriteString("\n\n**Provisioned platform resources** — the underlying technology behind each " +
				"resource above (the env-var bindings are already in the yaml block; this identifies what " +
				"they connect to, e.g. for SDK-docs lookups). Never hardcode values, only read the env " +
				"vars above:\n\n")
			sb.WriteString(resourceNotes)
		}
		sections = append(sections, sb.String())
	}
	if len(sections) == 0 {
		return ""
	}
	header := fmt.Sprintf("**Platform-resolved dependencies** — `%s` is provisioned, so its address now "+
		"exists.\n\nEach block below belongs to ONE component, named in its heading. Add the block to that "+
		"component's `workload.yaml` (merging into any existing `dependencies:`) **verbatim** — the platform "+
		"has already resolved the targets and the env-var bindings. OpenChoreo injects the resolved "+
		"addresses/outputs into your pod env at runtime; never hardcode them. A component with no block "+
		"here has no consumer-side dependencies.", depName)
	return header + "\n\n" + strings.Join(sections, "\n\n")
}

// resolveDependenciesYAML resolves one component's consumer deps — cross-project
// org-service endpoints, same-project component siblings, bound external-resource
// outputs, and platform-resource outputs — into the workload.yaml dependencies
// block, plus two human-readable sections: a "Consumed API contract" section
// (one per org-service/same-project endpoint dep, plus one per `external` dep
// with a design-time-collected spec) instructing the coding agent to fetch or
// read the provider's real contract instead of guessing at endpoints; and a
// "Provisioned platform resources" section (one per platform-resource dep)
// naming the resourceType + catalog description — the coding agent's only
// handle to identify the underlying technology, since the OC WorkloadDescriptor
// resources schema (workloadResourceDepYAML) has no room for either.
//
// Anything not yet resolvable is simply OMITTED: a later gate resolution
// re-posts the fuller block. Returns "" for the yaml block when nothing
// resolves. orgID is the OC namespace (orgHandle).
func (s *Service) resolveDependenciesYAML(ctx context.Context, orgID, projectID string, comp spec.DesignComponent) (yamlBlock, contracts, resourceNotes string, err error) {
	var deps workloadDeps
	var contractSections []string
	var resourceSections []string

	if s.providers != nil {
		// org-service endpoints (cross-project, visibility namespace). Skip any
		// provider not yet published namespace-visible — the cascade re-drives.
		for _, name := range comp.OrgServiceDependsOn() {
			target, ok, rerr := s.providers.ResolveNamespaceVisible(ctx, orgID, name)
			if rerr != nil {
				return "", "", "", fmt.Errorf("resolve org-service %q: %w", name, rerr)
			}
			if !ok {
				continue
			}
			deps.Endpoints = append(deps.Endpoints, workloadEndpointDepYAML{
				Project:     target.Project,
				Component:   target.Component,
				Name:        target.Name,
				Visibility:  "namespace",
				EnvBindings: map[string]string{"address": orgServiceURLEnv(name)},
			})
			contractSections = append(contractSections, orgServiceContractSection(name, target))
		}
		// same-project component siblings (visibility project). The sibling's OC
		// component name is `<project>-<logicalName>`; the env var keys on the
		// LOGICAL dep name. Project is omitted (same project).
		for _, depName := range comp.ComponentDependsOn() {
			ocComponent := projectID + "-" + depName
			target, ok, rerr := s.providers.ResolveProjectEndpoint(ctx, orgID, projectID, ocComponent)
			if rerr != nil {
				return "", "", "", fmt.Errorf("resolve same-project component %q: %w", depName, rerr)
			}
			if !ok {
				continue
			}
			deps.Endpoints = append(deps.Endpoints, workloadEndpointDepYAML{
				Component:   target.Component,
				Name:        target.Name,
				Visibility:  "project",
				EnvBindings: map[string]string{"address": orgServiceURLEnv(depName)},
			})
			contractSections = append(contractSections, localComponentContractSection(depName))
		}
	}

	// external deps with a design-time-collected spec (specPath set): tell the
	// coding agent to implement the client against that EXACT stored contract.
	// This is independent of the external resource's binding/provisioning state
	// below — the spec is a static repo artifact from design save, not a runtime
	// resolution, so it applies whether or not the connection is bound yet.
	for _, d := range comp.Dependencies {
		if d.Kind == spec.DependencyKindExternal && d.SpecPath != "" {
			contractSections = append(contractSections, externalSpecContractSection(d.Name, d.SpecPath))
		}
	}

	if s.bindings != nil {
		// external-resource outputs. Outputs are pre-namespaced by the resource
		// schema, so env-var name == output name verbatim. Skip any not provisioned.
		for _, name := range externalDepNames(comp) {
			envBindings, ok := s.bindingEnvBindings(ctx, orgID, projectID, name, false)
			if !ok {
				continue
			}
			deps.Resources = append(deps.Resources, workloadResourceDepYAML{
				Ref:         dependencies.ExternalResourceName(projectID, name),
				EnvBindings: envBindings,
			})
		}
		// platform-resource outputs. Outputs are generic (host, port, …), so they
		// are prefixed with the dep name. Binding + ref names share the
		// external-resource `<project>-<name>` form.
		for _, depName := range platformDepNames(comp) {
			envBindings, ok := s.bindingEnvBindings(ctx, orgID, projectID, depName, true)
			if !ok {
				continue
			}
			deps.Resources = append(deps.Resources, workloadResourceDepYAML{
				Ref:         dependencies.ExternalResourceName(projectID, depName),
				EnvBindings: envBindings,
			})
			if dep, found := findDependency(comp, depName, spec.DependencyKindPlatformResource); found {
				resourceSections = append(resourceSections, platformResourceIdentitySection(dep, envBindings))
			}
		}
	}

	if len(deps.Endpoints) == 0 && len(deps.Resources) == 0 {
		return "", "", "", nil
	}
	out, err := yaml.Marshal(map[string]workloadDeps{"dependencies": deps})
	if err != nil {
		return "", "", "", fmt.Errorf("marshal dependencies yaml: %w", err)
	}
	return string(out), strings.Join(contractSections, "\n\n"), strings.Join(resourceSections, "\n\n"), nil
}

// bindingEnvBindings reads a dependency's provisioned binding outputs and maps
// each to the env var OpenChoreo injects it as. ok is false when the binding is
// absent or carries no outputs yet (not provisioned — omit it).
//
// prefixed distinguishes the two output vocabularies: an external resource's
// outputs are already namespaced by its schema, so the env var IS the output
// name; a platform resource's are generic (host, port, …) and must be prefixed
// with the dependency name.
func (s *Service) bindingEnvBindings(ctx context.Context, orgID, projectID, depName string, prefixed bool) (map[string]string, bool) {
	b, err := s.bindings.GetBinding(ctx, orgID, dependencies.ExternalResourceBindingName(projectID, depName, defaultEnv))
	if err != nil || b == nil || b.Status == nil || len(b.Status.Outputs) == 0 {
		return nil, false
	}
	out := make(map[string]string, len(b.Status.Outputs))
	for _, o := range b.Status.Outputs {
		if prefixed {
			out[o.Name] = envVarName(depName, o.Name)
			continue
		}
		out[o.Name] = o.Name
	}
	return out, true
}

// orgServiceContractSection renders the "Consumed API contract" guidance for a
// resolved cross-project org-service dependency: it names the provider (from the
// already-resolved openchoreo.WorkloadEndpointInfo) and tells the coding agent to
// fetch the real contract via MCP rather than guessing at endpoints.
//
// Owner/repo/subdir are intentionally omitted: WorkloadEndpointInfo carries only
// Project/Component/Name/Type/Port/BasePath/Schema. Those coordinates live on the
// separate endpoints.OrgComponentEndpoint DTO behind list_org_component_endpoints,
// which the agent calls anyway and which returns them directly — resolving them
// here too would duplicate a lookup the agent is about to make itself.
func orgServiceContractSection(depName string, target openchoreo.WorkloadEndpointInfo) string {
	return fmt.Sprintf(
		"### Consumed API contract — %s\n"+
			"Provider: project `%s`, component `%s`, endpoint `%s`.\n"+
			"Call the `list_org_component_endpoints` MCP tool to get this provider's API "+
			"contract (inline spec, or via `get_remote_git_file_contents`/`search_remote_git_code` "+
			"when the spec lives in the repo). Implement the client against the EXACT operations. "+
			"Do NOT invent endpoints.",
		depName, target.Project, target.Component, target.Name,
	)
}

// localComponentContractSection renders the "local" variant of the "Consumed API
// contract" guidance for a same-project component dependency: the sibling's
// OpenAPI contract is checked out alongside this component's own code (same
// repo), so no MCP round-trip is needed.
func localComponentContractSection(depName string) string {
	return fmt.Sprintf(
		"### Consumed API contract — %s (local)\n"+
			"Provider: sibling component `%s` in this same project — no MCP call needed, its "+
			"contract is in your own checked-out repo.\n"+
			"Read `specs/design/components/%s/openapi.yaml` and implement the client against the "+
			"EXACT operations. Do NOT invent endpoints.",
		depName, depName, depName,
	)
}

// externalSpecContractSection renders the contract note for an `external`
// dependency that has a `specPath` — which is EITHER a URL or a repo-relative
// file path. It is the authoritative contract when present; the coding agent
// fetches it (URL) or reads it (file) and researches the API's own docs for
// anything the contract doesn't cover.
func externalSpecContractSection(depName, specPath string) string {
	return fmt.Sprintf(
		"External API contract for `%s`: `%s` — if this is a URL, fetch it; if a path, "+
			"it is a file in your checked-out repo. Use it as the source of truth for the "+
			"API's operations, and research the provider's docs for anything it doesn't cover.",
		depName, specPath,
	)
}

// platformResourceIdentitySection renders the resourceType + catalog description
// identification note for a provisioned platform-resource dependency. The
// outputs→env mapping is already visible in the yaml block's envBindings; this
// supplies the two facts the OC WorkloadDescriptor resources schema
// (workloadResourceDepYAML) has no room for — resourceType and the
// architect-recorded catalog description — which together are the coding agent's
// ONLY handle to identify the underlying technology behind the resource (needed
// for the SDK-docs lookup guardrail).
func platformResourceIdentitySection(dep spec.Dependency, envBindings map[string]string) string {
	desc := dep.Description
	if desc == "" {
		desc = "no catalog description recorded"
	}
	envs := make([]string, 0, len(envBindings))
	for _, env := range envBindings {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	return fmt.Sprintf(
		"### Platform resource — %s\nResource type: `%s` — %s.\nOutputs → env: %s.",
		dep.Name, dep.ResourceType, desc, strings.Join(envs, ", "),
	)
}

// findDependency looks up the single dependency entry matching name+kind — used
// where a caller already has the bare name (from platformDepNames) but needs the
// full entry's kind-specific fields (ResourceType/Description).
func findDependency(c spec.DesignComponent, name string, kind spec.DependencyKind) (spec.Dependency, bool) {
	for _, d := range c.Dependencies {
		if d.Kind == kind && d.Name == name {
			return d, true
		}
	}
	return spec.Dependency{}, false
}

func externalDepNames(c spec.DesignComponent) []string {
	var out []string
	for _, d := range c.Dependencies {
		if d.Kind == spec.DependencyKindExternal {
			out = append(out, d.Name)
		}
	}
	return out
}

func platformDepNames(c spec.DesignComponent) []string {
	var out []string
	for _, d := range c.Dependencies {
		if d.Kind == spec.DependencyKindPlatformResource {
			out = append(out, d.Name)
		}
	}
	return out
}

// orgServiceURLEnv mirrors endpoints.OrgServiceURLEnv: <UPPER_SNAKE>_URL (a local
// copy keeps this feature's edge to dependencies/endpoints out). e.g.
// "employee-api" → "EMPLOYEE_API_URL".
func orgServiceURLEnv(name string) string {
	return envVarName(name, "") + "URL"
}

// envVarName builds a valid C_IDENTIFIER env-var name from a dep name + output
// name. Delegates to dependencies.EnvVarName — the single source of truth shared
// with runtimeconfig's window._env_ keys, so the pod env var and the SPA config
// key for the same dep+output are byte-identical. "orders-db" + "host" →
// "ORDERS_DB_HOST".
func envVarName(depName, outName string) string {
	return dependencies.EnvVarName(depName, outName)
}
