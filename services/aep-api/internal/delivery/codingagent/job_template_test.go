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

import "testing"

// validJobInputs is the minimal input set that passes validate().
func validJobInputs() JobInputs {
	return JobInputs{
		RunName:             "ca-abc123-2601011200",
		OrgNS:               "wc-org-remote-worker",
		TaskID:              "exec-1",
		OrgID:               "org1",
		ProjectID:           "proj1",
		ComponentName:       "hello-world-api",
		RunnerImage:         "aep/remote-worker:latest",
		ServiceAccountName:  "runner-sa",
		AnthropicSecretName: "anthropic-secret",
		GitHubSecretName:    "github-secret",
		RepoURL:             "https://github.com/acme/hello-world-api",
		Prompt:              "work the issue",
		IdentityName:        "AEP Bot",
		IdentityEmail:       "bot@aep.dev",
		GitServiceURL:       "http://aep-api:9090",
		CallbackURL:         "http://aep-api:9090",
	}
}

// jobEnv extracts the runner container's env list as name→value from a built
// Job manifest, failing the test if the nested shape is unexpected.
func jobEnv(t *testing.T, job map[string]any) map[string]string {
	t.Helper()
	spec, _ := job["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]map[string]any)
	if len(containers) == 0 {
		t.Fatalf("no containers in job manifest: %+v", job)
	}
	envList, _ := containers[0]["env"].([]map[string]any)
	out := map[string]string{}
	for _, e := range envList {
		name, _ := e["name"].(string)
		val, _ := e["value"].(string)
		out[name] = val
	}
	return out
}

// jobImage extracts the runner container's image from a built Job manifest.
func jobImage(t *testing.T, job map[string]any) string {
	t.Helper()
	spec, _ := job["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]map[string]any)
	if len(containers) == 0 {
		t.Fatalf("no containers in job manifest: %+v", job)
	}
	img, _ := containers[0]["image"].(string)
	return img
}

// TestBuild_OneImageServesBothTaskKinds pins the collapsed image selection: an
// implementation Job and a validation Job render the SAME image and differ only
// in AEP_TASK_KIND (plus the component sentinel and deadline the executor sets).
// The retired split had a second, Playwright-only image because the alpine
// coding image could not run chromium; the one Debian image can.
func TestBuild_OneImageServesBothTaskKinds(t *testing.T) {
	const image = "ghcr.io/wso2/aep/remote-worker:1.2.3"

	impl := validJobInputs()
	impl.RunnerImage = image

	val := validJobInputs()
	val.RunnerImage = image
	val.TaskKind = "validation"
	val.ComponentName = "aep-validation"
	val.ActiveDeadlineSeconds = 7200

	implJob, err := Build(impl)
	if err != nil {
		t.Fatalf("Build(implementation): %v", err)
	}
	valJob, err := Build(val)
	if err != nil {
		t.Fatalf("Build(validation): %v", err)
	}

	if got := jobImage(t, implJob); got != image {
		t.Errorf("implementation image = %q, want %q", got, image)
	}
	if got := jobImage(t, valJob); got != image {
		t.Errorf("validation image = %q, want the same image %q", got, image)
	}
	if got := jobEnv(t, implJob)["AEP_TASK_KIND"]; got != "implementation" {
		t.Errorf("AEP_TASK_KIND (implementation) = %q, want %q", got, "implementation")
	}
	if got := jobEnv(t, valJob)["AEP_TASK_KIND"]; got != "validation" {
		t.Errorf("AEP_TASK_KIND (validation) = %q, want %q", got, "validation")
	}
}

// TestBuild_StampsSkillsRepoURLWhenSet pins the new optional AEP_SKILLS_REPO_URL
// env: present with the clone URL when the org's skills repo resolved.
func TestBuild_StampsSkillsRepoURLWhenSet(t *testing.T) {
	in := validJobInputs()
	in.SkillsRepoURL = "https://github.com/acme/org-skills"

	job, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := jobEnv(t, job)
	if got := env["AEP_SKILLS_REPO_URL"]; got != in.SkillsRepoURL {
		t.Errorf("AEP_SKILLS_REPO_URL = %q, want %q", got, in.SkillsRepoURL)
	}
}

// TestBuild_OmitsSkillsRepoURLWhenEmpty pins the degrade contract: an
// unprovisioned org (empty URL) stamps no env var, so the runner falls back to
// the base plugin rather than cloning "".
func TestBuild_OmitsSkillsRepoURLWhenEmpty(t *testing.T) {
	in := validJobInputs() // SkillsRepoURL left empty

	job, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := jobEnv(t, job)
	if _, present := env["AEP_SKILLS_REPO_URL"]; present {
		t.Errorf("AEP_SKILLS_REPO_URL must be absent when SkillsRepoURL is empty, got %q", env["AEP_SKILLS_REPO_URL"])
	}
}

// TestBuild_StampsMCPTokenAndURL pins the coding-agent MCP wiring (task B1):
// the rendered Job carries a dedicated AEP_MCP_TOKEN (aud aep-api-mcp, distinct
// from AEP_BEARER's git-service audience) and an AEP_MCP_URL derived from the
// BFF callback URL, so the runner pod can call the BFF's internal MCP surface
// (POST /internal/v1/mcp) for endpoint discovery / remote-file / code-search.
func TestBuild_StampsMCPTokenAndURL(t *testing.T) {
	in := validJobInputs()
	in.CallbackURL = "http://aep-api:9090"
	in.MCPToken = "mcp-token-xyz"

	job, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := jobEnv(t, job)
	if got, want := env["AEP_MCP_TOKEN"], "mcp-token-xyz"; got != want {
		t.Errorf("AEP_MCP_TOKEN = %q, want %q", got, want)
	}
	if got, want := env["AEP_MCP_URL"], "http://aep-api:9090/internal/v1/mcp"; got != want {
		t.Errorf("AEP_MCP_URL = %q, want %q", got, want)
	}
}

// TestBuild_OmitsMCPTokenWhenEmpty mirrors the AEP_BEARER contract: an empty
// MCPToken (minting failed / not wired) stamps no AEP_MCP_TOKEN env, but
// AEP_MCP_URL still renders unconditionally (parity with AEP_PLATFORM_URL) —
// the runner can then tell "no MCP token" apart from "empty token value".
func TestBuild_OmitsMCPTokenWhenEmpty(t *testing.T) {
	in := validJobInputs() // MCPToken left empty
	in.CallbackURL = "http://aep-api:9090"

	job, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := jobEnv(t, job)
	if _, present := env["AEP_MCP_TOKEN"]; present {
		t.Errorf("AEP_MCP_TOKEN must be absent when MCPToken is empty, got %q", env["AEP_MCP_TOKEN"])
	}
	if got, want := env["AEP_MCP_URL"], "http://aep-api:9090/internal/v1/mcp"; got != want {
		t.Errorf("AEP_MCP_URL = %q, want %q (must render even without a token)", got, want)
	}
}
