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
	"crypto/sha1" //nolint:gosec // git object names are SHA-1 by definition
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

const (
	// specsPrefix scopes the whole Files API — only paths under specs/ are
	// readable or writable through it.
	specsPrefix = "specs/"
	// maxFileBytes caps a single written file. specs/ artifacts (markdown, DSL,
	// component design.json, rendered excalidraw scenes) stay well under this.
	maxFileBytes = 5 << 20 // 5 MiB
	// casAttempts bounds Mutate's fast-forward retry on a concurrent writer
	// (passed as the RetryPolicy attempt count).
	casAttempts = 4
)

// blobSHA computes the git blob object name of content — SHA-1 over
// "blob <len>\x00" + content, exactly what `git hash-object` produces for the
// blobs Mutate stages. The apply response reports it per written file so the
// FE's next baseSha matches what a subsequent read (ls-tree) returns.
func blobSHA(content []byte) string {
	h := sha1.New() //nolint:gosec // git object names are SHA-1 by definition
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// validatePath enforces the specs/ scope: repo-relative, canonical, no
// traversal, under specs/. Mirrors the artifacts working-tree validator so the
// two agree on what "a spec file" is.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty path", ErrPathInvalid)
	}
	clean := path.Clean(p)
	if clean != p {
		return fmt.Errorf("%w: non-canonical path %q", ErrPathInvalid, p)
	}
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
		return fmt.Errorf("%w: must be repo-relative under specs/", ErrPathInvalid)
	}
	if !strings.HasPrefix(clean, specsPrefix) {
		return fmt.Errorf("%w: only specs/ paths are accessible via this API", ErrPathInvalid)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: traversal in path", ErrPathInvalid)
		}
	}
	return nil
}
