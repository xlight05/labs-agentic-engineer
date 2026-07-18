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

// CRTMarkers is spec's own vocabulary for the ClusterResourceType metadata
// markers design-save reads: the PE-authored role / skill / consumer-URL
// annotations projected off a resourceType. It is the consumer-side projection
// of the dependencies domain's marker catalog, reached through the
// resourceMarkerCatalog port and mapped at the composition root — so the spec
// domain names the dependencies feature nowhere (the P2c "a domain names no
// other domain's entity, even in a port" rule). dependencies becomes a domain
// in P8; this stays a port either way.
type CRTMarkers struct {
	// EndUserAuth is true when the resourceType carries the end-user-auth role
	// marker; design-save derives an end-user-auth dependency from it.
	EndUserAuth bool
	// ConsumerURLEnvConfig is the env-config key to patch the consumer's
	// callback URL into, or "" when the type carries no such annotation.
	ConsumerURLEnvConfig string
	// ConsumerURLPath is the path appended to the consumer's origin for that
	// patch.
	ConsumerURLPath string
	// Skill is the skill name that must appear in skillsApplied, or "".
	Skill string
	// Description is the human prose describing what the type provides, or "".
	Description string
}
