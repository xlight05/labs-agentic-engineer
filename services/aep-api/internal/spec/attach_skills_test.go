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
	"reflect"
	"testing"

	"github.com/wso2/aep/aep-api/models"
)

// --- attachAnnotatedSkills (pure function) -----------------------------------

// resourceDep builds a platform-resource dependency of the given resourceType.
func resourceDep(name, resourceType string) models.Dependency {
	return models.Dependency{Kind: models.DependencyKindPlatformResource, Name: name, ResourceType: resourceType}
}

// skillMarker returns a marker map flagging resourceType as carrying the
// given skill annotation.
func skillMarker(resourceType, skill string) map[string]CRTMarkers {
	return map[string]CRTMarkers{resourceType: {Skill: skill}}
}

// (a) dep with Skill marker → the owning component's SkillsApplied gains it.
func TestAttachAnnotatedSkills_AttachesToOwningComponent(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name:         "storefront-web",
			Dependencies: []models.Dependency{resourceDep("user-auth", "thunder-app")},
		}},
	}
	changed := attachAnnotatedSkills(d, skillMarker("thunder-app", "thunder-authentication"))
	if !reflect.DeepEqual(changed, []string{"storefront-web"}) {
		t.Fatalf("changed = %v, want [storefront-web]", changed)
	}
	if want := []string{"thunder-authentication"}; !equalStrings(d.Components[0].SkillsApplied, want) {
		t.Fatalf("SkillsApplied = %v, want %v", d.Components[0].SkillsApplied, want)
	}
}

// (b) already present on the component → no duplicate, not reported changed.
func TestAttachAnnotatedSkills_NoDuplicateWhenAlreadyPresent(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name:          "storefront-web",
			SkillsApplied: []string{"thunder-authentication"},
			Dependencies:  []models.Dependency{resourceDep("user-auth", "thunder-app")},
		}},
	}
	changed := attachAnnotatedSkills(d, skillMarker("thunder-app", "thunder-authentication"))
	if changed != nil {
		t.Fatalf("want changed=nil — skill already present, got %v", changed)
	}
	if want := []string{"thunder-authentication"}; !equalStrings(d.Components[0].SkillsApplied, want) {
		t.Fatalf("SkillsApplied = %v, want %v (no duplicate)", d.Components[0].SkillsApplied, want)
	}
}

// (c) unannotated type → untouched.
func TestAttachAnnotatedSkills_UnannotatedTypeUntouched(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name:         "orders-api",
			Dependencies: []models.Dependency{resourceDep("orders-db", "postgres-cnpg")},
		}},
	}
	// postgres-cnpg carries no skill annotation.
	changed := attachAnnotatedSkills(d, map[string]CRTMarkers{"postgres-cnpg": {}})
	if changed != nil {
		t.Fatalf("want changed=nil — type carries no skill annotation, got %v", changed)
	}
	if len(d.Components[0].SkillsApplied) != 0 {
		t.Fatalf("SkillsApplied = %v, want empty", d.Components[0].SkillsApplied)
	}
}

// (d) multiple deps on the SAME component with the same skill → one entry.
func TestAttachAnnotatedSkills_MultipleDepsSameSkillOneEntry(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name: "orders-api",
			Dependencies: []models.Dependency{
				resourceDep("user-auth", "thunder-app"),
				resourceDep("service-auth", "thunder-app"),
			},
		}},
	}
	changed := attachAnnotatedSkills(d, skillMarker("thunder-app", "thunder-authentication"))
	if !reflect.DeepEqual(changed, []string{"orders-api"}) {
		t.Fatalf("changed = %v, want [orders-api]", changed)
	}
	if want := []string{"thunder-authentication"}; !equalStrings(d.Components[0].SkillsApplied, want) {
		t.Fatalf("SkillsApplied = %v, want exactly one entry %v", d.Components[0].SkillsApplied, want)
	}
}

// (e) existing per-component entries preserved verbatim, append-only ordering.
func TestAttachAnnotatedSkills_PreservesExistingEntriesVerbatim(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name:          "storefront-web",
			SkillsApplied: []string{"z-first-manual-skill", "a-second-manual-skill"},
			Dependencies:  []models.Dependency{resourceDep("user-auth", "thunder-app")},
		}},
	}
	changed := attachAnnotatedSkills(d, skillMarker("thunder-app", "thunder-authentication"))
	if !reflect.DeepEqual(changed, []string{"storefront-web"}) {
		t.Fatalf("changed = %v, want [storefront-web]", changed)
	}
	want := []string{"z-first-manual-skill", "a-second-manual-skill", "thunder-authentication"}
	if !equalStrings(d.Components[0].SkillsApplied, want) {
		t.Fatalf("SkillsApplied = %v, want %v (existing entries first, untouched order)", d.Components[0].SkillsApplied, want)
	}
}

// Non-platform-resource dependency kinds must never qualify, even if their
// (meaningless) ResourceType field happens to collide with a marked type.
func TestAttachAnnotatedSkills_NonPlatformResourceDepIgnored(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name: "orders-api",
			Dependencies: []models.Dependency{
				{Kind: models.DependencyKindOrgService, Name: "billing", ResourceType: "thunder-app"},
			},
		}},
	}
	changed := attachAnnotatedSkills(d, skillMarker("thunder-app", "thunder-authentication"))
	if changed != nil {
		t.Fatalf("want changed=nil — org-service dependency must never qualify, got %v", changed)
	}
	if len(d.Components[0].SkillsApplied) != 0 {
		t.Fatalf("SkillsApplied = %v, want empty", d.Components[0].SkillsApplied)
	}
}

// A nil markers map (no platform-resource dependency in the design → no
// catalog fetch, see resourceMarkersForAuthDerivation) qualifies nothing.
func TestAttachAnnotatedSkills_NilMarkersNoop(t *testing.T) {
	t.Parallel()
	d := &DesignFile{
		Components: []models.DesignComponent{{
			Name:         "orders-api",
			Dependencies: []models.Dependency{resourceDep("user-auth", "thunder-app")},
		}},
	}
	if changed := attachAnnotatedSkills(d, nil); changed != nil {
		t.Fatalf("want changed=nil with a nil marker map, got %v", changed)
	}
	if len(d.Components[0].SkillsApplied) != 0 {
		t.Fatalf("SkillsApplied = %v, want empty", d.Components[0].SkillsApplied)
	}
}

// Per-component isolation: a dependency owned by one component must never
// spill a skill into a sibling component, even a sibling that depends ON the
// owning component (component-kind dependency, not platform-resource).
func TestAttachAnnotatedSkills_PerComponentIsolation(t *testing.T) {
	t.Parallel()
	df := &DesignFile{Components: []models.DesignComponent{
		{Name: "api", Dependencies: []models.Dependency{
			{Kind: models.DependencyKindPlatformResource, Name: "db", ResourceType: "postgres-cnpg"},
		}},
		{Name: "web", Dependencies: []models.Dependency{
			{Kind: models.DependencyKindComponent, Name: "api"},
		}},
	}}
	markers := map[string]CRTMarkers{"postgres-cnpg": {Skill: "postgres"}}

	changed := attachAnnotatedSkills(df, markers)

	if !reflect.DeepEqual(changed, []string{"api"}) {
		t.Fatalf("changed = %v, want [api]", changed)
	}
	if !reflect.DeepEqual(df.Components[0].SkillsApplied, []string{"postgres"}) {
		t.Fatalf("api skillsApplied = %v, want [postgres]", df.Components[0].SkillsApplied)
	}
	if len(df.Components[1].SkillsApplied) != 0 {
		t.Fatalf("web should gain no skills, got %v", df.Components[1].SkillsApplied)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
