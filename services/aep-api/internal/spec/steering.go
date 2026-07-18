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

// steeringByUseCase is the explicit action-steer appended to the instruction
// so the model loads the right skill(s) from the catalog and produces the
// right artifacts. Skills are NOT pushed inline anymore — the agents service
// reads the full org catalog (embedded flow skills seeded into the _skills
// repo + org skills) from the SkillsRef snapshot, and the model picks from it
// via loadSkill; the steering lines name the flows, not specific skills, so
// they survive the catalog switch unchanged in intent.
var steeringByUseCase = map[string]string{
	useCaseRequirementsGenerate: "\n\nGenerate the requirements document at specs/requirements/requirements.md from the direction above. Write clear, well-structured markdown.",
	useCaseRequirementsChat: "\n\nApply the requested change to the current requirements draft, or answer the question if no change is requested. " +
		"Requirements files live under specs/requirements/ (main document: specs/requirements/requirements.md) — when creating a file that does not exist yet, always use that full path, never a bare filename.",
	useCaseDesignGenerate: "\n\nGenerate the design under specs/design/ from the approved requirements in specs/requirements/. Load every relevant skill in ONE loadSkill call (including cell-architecture-dsl), then emit files in EXACTLY this order: " +
		"(1) specs/design/design.cell — the live architecture diagram (see the cell-architecture-dsl skill for grammar + boundary rules): write it in a SINGLE addFile; the platform streams it into the live diagram line by line as you write; " +
		"(2) a concise specs/design/design.md; (3) EVERY component's design.json — the component ids MUST match design.cell, and every design.cell edge touching a component MUST appear as a dependency in that component's design.json (and vice versa); (4) wireframes.dsl per web app; (5) openapi.yaml per service; " +
		"(6) specs/validation/validation-criteria.json LAST, per the validation-criteria skill — derived from the requirements prose ONLY, never from the design you just wrote; " +
		"when the file already exists, keep each unchanged criterion's id stable so previously-authored e2e specs still map. The skills define each artifact.",
	// useCaseGeneral is the no-useCase turn (the "useCase" field was omitted):
	// a generic spec turn that is NOT scoped to requirements or design. The
	// steering names neither flow — it only tells the model where the spec
	// sources live so new files land on their full path, and lets the
	// instruction alone decide what to touch.
	useCaseGeneral: "\n\nApply the requested change to the spec bundle, or answer the question if no change is requested. " +
		"Spec sources live under specs/ (requirements under specs/requirements/, design under specs/design/) — when creating a file that does not exist yet, always use its full path, never a bare filename.",
}

// collabDepsSteer is appended to every collab room-scoped turn (the Spec view
// authors design.json live under useCase "requirements-chat", so the per-useCase
// steering never names the design flow). It steers the collab architect to load
// the architecture skill and to DISCOVER real provider names before touching a
// component's dependencies — without it the MCP tool can sit present-but-unused,
// producing invented role-based org-service names that fail exact-name
// resolution at build.
const collabDepsSteer = "\n\nBefore editing any design.json, load the `high-level-architecture` skill. " +
	"When your change adds or edits a component's `dependencies` — especially an `org-service` referencing another project — " +
	"FIRST call `list_org_endpoints` and copy the provider component name VERBATIM; never invent a role-based name."
