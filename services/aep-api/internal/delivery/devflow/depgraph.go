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

package devflow

import "strings"

// PlannedTask is one task the plan step produced: its GitHub issue number, its
// component key (the node id in the dependency graph), and the component keys
// it depends on. Mirrors the funnel's latest-per-component dependency model
// (docs/design/tasks-github-native.md §5): edges run component → each dependsOn
// name, resolved case-insensitively.
type PlannedTask struct {
	Issue     int      `json:"issue"`
	Key       string   `json:"key"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// detectDepCycle returns a cycle path (component keys) if the dependency graph
// among the planned tasks contains one, else nil. This is the same DFS the
// execution funnel uses (funnel.detectCycle), lifted here so the dev workflow
// can fail fast before starting any child rather than leaving tasks queued
// behind an unsatisfiable gate. A dependsOn naming a component with no task is
// simply an unresolved edge (not a cycle) and is ignored here.
func detectDepCycle(tasks []PlannedTask) []string {
	byKey := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		byKey[strings.ToLower(t.Key)] = lowerAll(t.DependsOn)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var path []string
	var dfs func(node string) []string
	dfs = func(node string) []string {
		if visiting[node] {
			for i, n := range path {
				if n == node {
					return append(append([]string{}, path[i:]...), node)
				}
			}
			return append(append([]string{}, path...), node)
		}
		if visited[node] {
			return nil
		}
		deps, ok := byKey[node]
		if !ok {
			return nil // unresolved edge — not a cycle
		}
		visiting[node] = true
		path = append(path, node)
		for _, dep := range deps {
			if cyc := dfs(dep); len(cyc) > 0 {
				return cyc
			}
		}
		path = path[:len(path)-1]
		visiting[node] = false
		visited[node] = true
		return nil
	}
	for key := range byKey {
		if cyc := dfs(key); len(cyc) > 0 {
			return cyc
		}
	}
	return nil
}

// depsSatisfied reports whether every dependency of t has succeeded. A
// dependency that names a component with no task in this run is treated as
// satisfied (unresolved external edge — the funnel's own gate is the backstop
// at dispatch time).
func depsSatisfied(t PlannedTask, succeeded map[string]bool, present map[string]bool) bool {
	for _, dep := range t.DependsOn {
		k := strings.ToLower(dep)
		if present[k] && !succeeded[k] {
			return false
		}
	}
	return true
}

// depFailed reports whether any dependency of t failed (or was itself skipped).
func depFailed(t PlannedTask, failed map[string]bool) bool {
	for _, dep := range t.DependsOn {
		if failed[strings.ToLower(dep)] {
			return true
		}
	}
	return false
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}
