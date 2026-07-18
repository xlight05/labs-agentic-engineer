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

// validation-preview dry-runs the validation-issue minter against a local
// validation-criteria.json and prints the EXACT issue it would create (title,
// labels, body — machine block included). No GitHub call, no DB: the issue
// client is a stdout fake. Use it to iterate on the oracle → issue rendering,
// then create the real issue with your own gh:
//
//	go run ./cmd/validation-preview \
//	  -criteria ../agents/chat_playground/hello-ui/specs/validation/validation-criteria.json \
//	  -components hello-web,hello-api -project hello-ui -design-tag design-v1
//
//	# real issue (yours to run; labels must exist or use --label with gh >=2.4):
//	go run ./cmd/validation-preview -criteria ... -components ... -body-only > /tmp/body.md
//	gh issue create --repo <owner>/<repo> \
//	  --title "Validate the deployed system against its acceptance criteria" \
//	  --label aep:task --label aep:validation --label aep:origin/spec-plan --label aep:execute \
//	  --body-file /tmp/body.md
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/wso2/aep/aep-api/internal/delivery/validation"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// fileCriteria reads the criteria from the local path (validation.CriteriaReader).
type fileCriteria struct{ path string }

func (f fileCriteria) ReadValidationCriteria(context.Context, string, string) ([]byte, bool, error) {
	raw, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

// flagComponents serves the design components from the -components flag
// (validation.DesignReader).
type flagComponents struct{ names []string }

func (f flagComponents) ReadDesignComponents(context.Context, string, string) ([]spec.DesignComponent, error) {
	comps := make([]spec.DesignComponent, len(f.names))
	for i, n := range f.names {
		comps[i] = spec.DesignComponent{Name: n}
	}
	return comps, nil
}

// printIssues captures the CreateIssue call instead of hitting GitHub
// (validation.IssueClient). ListIssues is empty — no dedup hit on a dry-run.
type printIssues struct {
	created *sourcecontrol.CreateIssueRequest
}

func (p *printIssues) ListIssues(context.Context, string, string, []string) ([]sourcecontrol.IssueInfo, error) {
	return nil, nil
}

func (p *printIssues) CreateIssue(_ context.Context, _, _ string, req sourcecontrol.CreateIssueRequest) (*sourcecontrol.IssueResult, error) {
	*p.created = req
	return &sourcecontrol.IssueResult{Number: 0, URL: "(dry-run)"}, nil
}

func main() {
	criteria := flag.String("criteria", "", "path to validation-criteria.json (required)")
	components := flag.String("components", "", "comma-separated design component names the task dependsOn (required)")
	project := flag.String("project", "hello-ui", "project id (feeds the idempotency key)")
	designTag := flag.String("design-tag", "design-v1", "design tag (lineage + idempotency key)")
	bodyOnly := flag.Bool("body-only", false, "print only the issue body (pipe into gh issue create --body-file -)")
	flag.Parse()
	if *criteria == "" || *components == "" {
		flag.Usage()
		os.Exit(2)
	}

	var captured sourcecontrol.CreateIssueRequest
	svc := validation.NewService(validation.Deps{
		Issues:   &printIssues{created: &captured},
		Design:   flagComponents{names: strings.Split(*components, ",")},
		Criteria: fileCriteria{path: *criteria},
	})
	if err := svc.EnsureValidationIssue(context.Background(), "preview-org", *project, *designTag); err != nil {
		fmt.Fprintln(os.Stderr, "mint failed:", err)
		os.Exit(1)
	}
	if captured.Title == "" {
		// The minter skipped (absent or unusable criteria file) — its slog
		// warning above says why.
		fmt.Fprintln(os.Stderr, "minter skipped: no issue would be created (criteria absent or unusable)")
		os.Exit(1)
	}
	if *bodyOnly {
		fmt.Print(captured.Body)
		return
	}
	fmt.Println("── title ──")
	fmt.Println(captured.Title)
	fmt.Println("── labels ──")
	fmt.Println(strings.Join(captured.Labels, " "))
	fmt.Println("── body ──")
	fmt.Println(captured.Body)
}
