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

package dependencies

import "strings"

// OrgServiceURLEnv is the consumer env var OC binds the resolved org-service
// address to — the `<UPPER_SNAKE>_URL` convention SPAs read via window._env_,
// so a Go/Node consumer reads e.g. EMPLOYEE_API_URL regardless of how the URL
// is delivered. Single source of truth for that derivation: the dispatch-time
// consumer-dependency YAML renderer (a later task) calls this exported
// function directly rather than forking the logic.
func OrgServiceURLEnv(name string) string {
	var b strings.Builder
	prevAlnum := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			prevAlnum = true
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevAlnum = true
		case r == '-' || r == '_':
			if prevAlnum {
				b.WriteByte('_')
			}
			prevAlnum = false
		}
	}
	return strings.TrimSuffix(b.String(), "_") + "_URL"
}
