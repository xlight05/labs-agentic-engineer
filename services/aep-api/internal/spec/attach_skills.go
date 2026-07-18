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

package spec

import (
	"github.com/wso2/aep/aep-api/models"
)

// Generic conditional skill attachment (Task G4,
// learning/thunder-resource/PLAN-generalization.md): a `platform-resource`
// dependency whose ClusterResourceType carries the PE-authored
// `aep.wso2.com/skill` annotation (CRTMarkers.Skill) means the
// design needs that skill's agent instructions to work with the dependency —
// so design save ensures the skill name is present in the OWNING component's
// skillsApplied (per-component design.json — see
// models.DesignComponent.SkillsApplied). Membership keys ONLY on the CRT
// annotation, never on a resourceType name or component type: any dependency
// kind carrying the marker qualifies, exactly like deriveEndUserAuth keys on
// the role label rather than a hardcoded name.
//
// This is append-only by design: a component's skillsApplied may carry
// entries from other sources (generation-time skill selection, manual
// edits), and this pass must never remove or reorder them — it only ever
// adds a missing name, once, to the component that declared the dependency.
// An unresolvable/unknown skill name is deliberately NOT validated here: it is
// attached as-authored on the CRT, and the downstream resolve layer
// (skills.SkillService.ResolveMany, read at execution time via
// execution.SkillsService.SkillsForExecution) already tolerates a missing
// skill name by warning and omitting it rather than failing — see
// skills/repo_store.go's ResolveMany. A PE typo in the annotation must not
// brick every save of every design that happens to depend on that type.

// attachAnnotatedSkills ensures every skill named by a component's
// platform-resource dependency CRT annotation is present in THAT component's
// SkillsApplied (append-only, de-duplicated within the component). Returns the
// names of components that gained at least one skill. A nil/empty markers map
// (no platform-resource dependency in the design, or none of its types carry
// the annotation) changes nothing.
func attachAnnotatedSkills(designFile *DesignFile, markers map[string]CRTMarkers) []string {
	var changed []string
	for i := range designFile.Components {
		comp := &designFile.Components[i]
		present := make(map[string]struct{}, len(comp.SkillsApplied))
		for _, name := range comp.SkillsApplied {
			present[name] = struct{}{}
		}
		added := false
		for _, dep := range comp.Dependencies {
			if dep.Kind != models.DependencyKindPlatformResource {
				continue
			}
			skill := markers[dep.ResourceType].Skill
			if skill == "" {
				continue
			}
			if _, ok := present[skill]; ok {
				continue
			}
			present[skill] = struct{}{}
			comp.SkillsApplied = append(comp.SkillsApplied, skill)
			added = true
		}
		if added {
			changed = append(changed, comp.Name)
		}
	}
	return changed
}
