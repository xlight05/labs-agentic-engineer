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

// Org-config surface types (GET/PATCH /config — the consolidated llm /
// gitProvider / idp document). HAND-WRITTEN and excluded from contract
// codegen (exclude-schemas): ConfigPatch's sections are three-state
// patch.Field values (absent = keep / null = clear / value = replace) and the
// projections use pointer sections for the wire's null-means-not-connected —
// semantics the generator cannot express. Kept field-for-field aligned with
// packages/contracts/api/v1 (ConfigProjection, ConfigPatch, *Projection);
// gen-api-check pins the rest of the contract.
package models

import (
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/patch"
)

// --- Read side: ConfigProjection (no secret material) -----------------------

// ConfigProjection is the GET /config body: the org's connection state across
// all three sections. Secrets are never echoed (write-only), so this shape is
// intentionally distinct from ConfigPatch — which is what forces PATCH-not-PUT
// (a client can't round-trip a full document it can't read back).
//
// A null llm/gitProvider means "not connected" (replacing the legacy
// {status:"not_connected"} sentinel objects); idp is always present because an
// org always has at least the platform default.
type ConfigProjection struct {
	LLM         *LLMProjection         `json:"llm"`         // null = not connected
	GitProvider *GitProviderProjection `json:"gitProvider"` // null = not connected
	IDP         IDPProjection          `json:"idp"`         // always present
}

// LLMProjection carries the org's LLM connection status. Fields are carried 1:1
// from orgcreds.AnthropicProjection minus ocOrgId (dropped from all
// projections — the org is implicit from the JWT).
type LLMProjection struct {
	Kind            string     `json:"kind" enum:"anthropic"`
	KeyPrefix       string     `json:"keyPrefix"`
	KeyLast4        string     `json:"keyLast4"`
	Status          string     `json:"status"`
	ConnectedAt     time.Time  `json:"connectedAt"`
	LastValidatedAt *time.Time `json:"lastValidatedAt,omitempty"`
	ValidationError *string    `json:"validationError,omitempty"`
}

// GitProviderProjection carries the org's git-provider connection status. The
// legacy credential kind (user-pat / app-installation) is re-expressed as
// kind=github + mode=pat|app so the provider is role-named, not vendor-pathed.
type GitProviderProjection struct {
	Kind              string     `json:"kind" enum:"github"`
	Mode              string     `json:"mode" enum:"app,pat"`
	GitHubLogin       string     `json:"githubLogin,omitempty"`
	IdentityLogin     string     `json:"identityLogin,omitempty"`
	IdentityName      string     `json:"identityName,omitempty"`
	IdentityEmail     string     `json:"identityEmail,omitempty"`
	InstallationID    *int64     `json:"installationId,omitempty"` // app-mode
	SelectedRepos     []string   `json:"selectedRepos,omitempty"`  // app-mode
	Status            string     `json:"status"`
	ConnectedAt       time.Time  `json:"connectedAt"`
	LastValidatedAt   *time.Time `json:"lastValidatedAt,omitempty"`
	IdentityChangedAt *time.Time `json:"identityChangedAt,omitempty"`
	PrevIdentityLogin *string    `json:"prevIdentityLogin,omitempty"`
}

// IDPProjection carries the org's IDP profile. Fields are carried 1:1 from the
// idp feature's profileSummaryFields; the live client secret is never echoed
// (hasClientSecret reflects whether one is stored).
type IDPProjection struct {
	Kind              string `json:"kind" enum:"platform,asgardeo,custom"`
	Issuer            string `json:"issuer"`
	JWKSURL           string `json:"jwksUrl"`
	PublisherClientID string `json:"publisherClientId"`
	HasClientSecret   bool   `json:"hasClientSecret"`
}

// --- Write side: ConfigPatch (omittable-nullable sections) ------------------

// ConfigPatch is the PATCH /config body. Each section is a three-state
// patch.Field: absent = keep, null = clear (where allowed), present = replace
// the section wholesale (deliberately not RFC 7386 deep-merge — a section with
// write-only fields can't be deep-merged into).
type ConfigPatch struct {
	LLM         patch.Field[LLMWrite]         `json:"llm,omitempty"`
	GitProvider patch.Field[GitProviderWrite] `json:"gitProvider,omitempty"`
	IDP         patch.Field[IDPWrite]         `json:"idp,omitempty"`
}

// LLMWrite is the llm section's write shape. The apiKey is write-only: probed
// against Anthropic, never echoed in any projection.
type LLMWrite struct {
	Kind   string `json:"kind" enum:"anthropic" required:"true"`
	APIKey string `json:"apiKey" required:"true"`
}

// GitProviderWrite is the gitProvider section's write shape. Mode is pat-only:
// App-mode is driven by the connect-sessions action route (OAuth), so it is
// schema-rejected here (the enum has no "app" value), pointing the client at
// the right flow.
type GitProviderWrite struct {
	Kind        string `json:"kind" enum:"github" required:"true"`
	Mode        string `json:"mode" enum:"pat" required:"true"`
	PAT         string `json:"pat" required:"true"` // write-only, probed, never echoed
	GitHubLogin string `json:"githubLogin,omitempty"`
}

// IDPWrite is the idp section's write shape. issuer/jwksUrl are optional: the
// section is replaced wholesale, so an omitted jwksUrl clears it (there is no
// legacy empty-string-means-keep carry-over — org-config-consolidation.md §4).
type IDPWrite struct {
	Kind    string `json:"kind" enum:"platform,asgardeo,custom" required:"true"`
	Issuer  string `json:"issuer,omitempty"`
	JWKSURL string `json:"jwksUrl,omitempty"`
}
