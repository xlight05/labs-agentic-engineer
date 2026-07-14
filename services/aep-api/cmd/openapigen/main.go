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

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wso2/aep/aep-api/internal/api"
)

func main() {
	specs := []struct {
		path string
		gen  func() ([]byte, error)
	}{
		// The curated contracts (packages/contracts/api/{v1,internal/v1}) are the
		// hand-maintained source of truth and are NEVER written by this tool.
		// This export renders what remains Huma-REGISTERED (unmigrated features)
		// as a comparison aid under the gitignored build/; the internal surface
		// is contract-first now, so its export is gone. The whole tool dies with
		// the last *_huma.go (contract-first migration, issue 005).
		{filepath.Join("build", "public-openapi.yaml"), api.GenerateOpenAPIYAML},
	}
	for _, s := range specs {
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "mkdir", filepath.Dir(s.path)+":", err)
			os.Exit(1)
		}
		b, err := s.gen()
		if err != nil {
			fmt.Fprintln(os.Stderr, "generate", s.path+":", err)
			os.Exit(1)
		}
		if err := os.WriteFile(s.path, b, 0o644); err != nil { //nolint:gosec
			fmt.Fprintln(os.Stderr, "write", s.path+":", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d bytes)\n", s.path, len(b))
	}
}
