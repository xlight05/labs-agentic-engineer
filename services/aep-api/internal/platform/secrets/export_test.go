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

package secrets

// export_test.go is the standard Go seam that lets the BLACK-BOX resolver
// dbtest (package secrets_test — black-box because a white-box one would cycle
// through platform/dbtest -> migrate -> a domain) assert on the two resolver
// mappings that have no other exported observable. It is a `_test.go` file, so
// it exists ONLY in the test binary — zero production API surface.

// InstallationIDForTest reports the installation id a resolved app-installation
// credential will mint against (Token -> minter.MintForInstallation), which is
// the App-side tenant-scoping key derived from org_credentials.installation_id.
// ok is false for any other credential kind. Test-only.
func InstallationIDForTest(c Credential) (id int64, ok bool) {
	a, isApp := c.(*appInstallationCred)
	if !isApp {
		return 0, false
	}
	return a.installationID, true
}

// OcOrgIDForTest reports the ocOrgID a resolved user-PAT credential is scoped to
// (the resolver's own input, threaded onto the credential). ok is false for any
// other credential kind. Test-only.
func OcOrgIDForTest(c Credential) (ocOrgID string, ok bool) {
	p, isPAT := c.(*userPATCred)
	if !isPAT {
		return "", false
	}
	return p.ocOrgID, true
}
