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

// Package patch provides the omittable-nullable field wrapper that PATCH-style
// request bodies need. A plain *T can only tell "present" from "absent"; a JSON
// merge PATCH also has to distinguish an explicit `null` (clear the value) from
// an omitted key (leave it untouched). Field[T] captures all three states so a
// handler can branch on Sent/Null.
//
// A custom json.Unmarshaler records whether the key was sent and whether it
// was null; the contract declares the matching `nullable` section schemas, so
// the request validator accepts all three states. See
// docs/design/org-config-consolidation.md §4.
package patch

import (
	"bytes"
	"encoding/json"
)

// Field is a three-state PATCH field wrapping a value of type T.
//
//   - key absent from the body      → Sent=false            (keep existing)
//   - key present and JSON null      → Sent=true, Null=true  (clear)
//   - key present with a value       → Sent=true, Null=false (replace with Value)
//
// The zero value is the "absent" state, which is what a struct field left
// unpopulated by json.Unmarshal reports — so a handler reads `.Sent` to decide
// whether a section was touched at all.
type Field[T any] struct {
	Sent  bool
	Null  bool
	Value T
}

// UnmarshalJSON records the tri-state. json.Unmarshal only calls this when the
// key is present in the object, so a false Sent unambiguously means "absent".
// An explicit `null` sets Null without touching Value; any other token decodes
// into Value.
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	f.Sent = true
	if bytes.Equal(b, []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.Value)
}
