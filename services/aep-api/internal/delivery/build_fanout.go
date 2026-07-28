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

package delivery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The merged-pull-request → builds contract, shared by the two halves of the
// milestone loop that must agree on it exactly: the EVENT PLANE triggers the
// fan-out, and the RUN SUPERVISOR reads it back to decide whether the cycle
// landed green. They are peer sub-packages that may not import each other, so
// "which components a merge rebuilds" and "what those runs are called" live
// here — one definition, no drift.

// PathDiff is the outcome of matching a merged pull request's changed files
// against the design's App Paths: the components to build, and the files that
// belong to no component.
type PathDiff struct {
	Components []string
	Unmatched  []string
}

// DiffComponents maps changed files onto components by App Path prefix.
//
// It is generic over who authored the pull request — the merge, not the
// authorship, is what makes a component stale. Unmatched files are returned
// rather than dropped so the caller can WARN about them: a path outside every
// App Path is either a repo-root concern (docs, CI) or a design that has
// drifted from the tree, and silently ignoring the second is how a component
// stops being rebuilt without anybody noticing.
//
// Components are returned in a stable order so a fan-out is reproducible — and
// so the supervisor's expected set is the same list the trigger built.
func DiffComponents(files []string, appPaths map[string]string) PathDiff {
	var out PathDiff
	if len(files) == 0 {
		return out
	}
	claimed := make(map[string]bool, len(files))
	for name, appPath := range appPaths {
		hit := false
		for _, f := range files {
			if fileUnder(f, appPath) {
				claimed[f] = true
				hit = true
			}
		}
		if hit {
			out.Components = append(out.Components, name)
		}
	}
	sort.Strings(out.Components)
	for _, f := range files {
		if !claimed[f] {
			out.Unmatched = append(out.Unmatched, f)
		}
	}
	return out
}

// fileUnder reports whether a changed file lives under a component's App Path.
// An empty App Path means the component builds from the repo root, so every
// change is its change.
func fileUnder(file, appPath string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(appPath), "./")
	clean = strings.Trim(clean, "/")
	if clean == "" {
		return true
	}
	return file == clean || strings.HasPrefix(file, clean+"/")
}

// ShortSHA is the 12-hex-character form of a commit used in run names, dedupe
// keys and issue prose. Twelve is git's own long-enough-to-be-unique default
// and keeps a WorkflowRun name inside the Kubernetes name budget.
func ShortSHA(sha string) string {
	s := strings.ToLower(strings.TrimSpace(sha))
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// BuildRunNamePrefix is the (component, commit) half of a build WorkflowRun's
// name — the key both the automatic re-trigger budget and the supervisor's
// cycle-build read count on. Attempts share it and differ only in the trailing
// ordinal, so counting the runs whose name carries this prefix IS the attempt
// count, derived from OpenChoreo rather than stored anywhere.
func BuildRunNamePrefix(projectID, component, sha string) string {
	return strings.ToLower(fmt.Sprintf("%s-%s-%s-", projectID, component, ShortSHA(sha)))
}

// BuildRunName names attempt n (1-based) of a component's build at a commit.
func BuildRunName(projectID, component, sha string, attempt int) string {
	return fmt.Sprintf("%s%d", BuildRunNamePrefix(projectID, component, sha), attempt)
}

// MergeBuild is one component's build at a merge SHA, read back off its
// WorkflowRun. Status and Completed are the run's own pair, carried verbatim:
// OpenChoreo's status is a condition Reason string rather than a closed set, so
// Completed is the terminal gate and Status is display only.
type MergeBuild struct {
	Component string
	RunName   string
	Status    string
	Completed bool
	StartedAt string
	// Attempt is the run name's trailing ordinal: 1 for the fan-out's build, 2
	// for the one automatic re-trigger a red build gets.
	Attempt int
}

// BuildsAtMerge picks the builds belonging to one merge out of a project's
// WorkflowRuns, newest attempt per component, ordered by component name.
//
// This is the READ-side inverse of BuildRunName, and it is a projection of the
// cluster rather than of anything stored — the same rule the re-trigger budget
// follows, for the same reason: a stored copy of per-component build state
// could desynchronise from the cluster, and a run that exists cannot be
// un-counted. Matching is on the name because the name is what carries the
// (component, commit, attempt) triple; the component is read off the run's own
// label rather than parsed back out, so a component whose name contains a dash
// cannot be mis-split.
func BuildsAtMerge(runs []MergeBuild, projectID, sha string) []MergeBuild {
	if sha == "" {
		return nil
	}
	best := map[string]MergeBuild{}
	for _, r := range runs {
		if r.Component == "" {
			continue
		}
		prefix := BuildRunNamePrefix(projectID, r.Component, sha)
		if !strings.HasPrefix(strings.ToLower(r.RunName), prefix) {
			continue
		}
		attempt, err := strconv.Atoi(strings.ToLower(r.RunName)[len(prefix):])
		if err != nil || attempt <= 0 {
			// A name that carries the prefix but no ordinal is not one of ours.
			continue
		}
		r.Attempt = attempt
		if prior, seen := best[r.Component]; !seen || attempt > prior.Attempt {
			best[r.Component] = r
		}
	}
	out := make([]MergeBuild, 0, len(best))
	for _, b := range best {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Component < out[j].Component })
	return out
}
