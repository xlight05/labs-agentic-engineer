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

package sourcecontrol

import (
	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
)

// TagInfo describes a git tag. Alias of the gitfs definition (identical
// fields/tags) so Workspace.ListTags results flow into the version-naming
// helpers unchanged.
type TagInfo = gitfs.TagInfo

// GitOpsService is the git-object gateway consumed by the artifacts, files,
// genai, skills, and task stores: the Workspace port (the disk-backed mirror
// engine), the credential resolver, and the save-identity helper.
type GitOpsService interface {
	ResolveSaveIdentities(cred secrets.Credential) (*GitIdentity, *GitIdentity)
	// Workspace is the mount-backed git engine (reads, Mutate, tags, diff).
	Workspace() Workspace
	Resolver() secrets.Resolver
}

type gitOpsService struct {
	resolver secrets.Resolver
	// workspace is the gitfs engine over the shared /workspaces mount.
	workspace Workspace
}

// NewGitOpsService builds the git-object gateway over the mount-backed
// Workspace engine every repo-content consumer runs on.
func NewGitOpsService(resolver secrets.Resolver, workspace Workspace) GitOpsService {
	return &gitOpsService{resolver: resolver, workspace: workspace}
}

// Workspace returns the mount-backed Workspace engine wired in at construction.
func (s *gitOpsService) Workspace() Workspace { return s.workspace }

// Resolver returns the credential resolver wired in at construction.
func (s *gitOpsService) Resolver() secrets.Resolver { return s.resolver }

// ResolveSaveIdentities returns (author, committer) identities for a save. Both
// are the credential identity, falling back to a default AEP name/email when the
// credential carries none.
func (s *gitOpsService) ResolveSaveIdentities(cred secrets.Credential) (*GitIdentity, *GitIdentity) {
	id := cred.Identity()
	if id.Name == "" {
		id.Name = "AEP"
	}
	if id.Email == "" {
		id.Email = "noreply@aep.dev"
	}
	// Two distinct pointers with identical fields: a caller mutating the author
	// (e.g. to override a co-author) must never alias the committer.
	author := &GitIdentity{Name: id.Name, Email: id.Email}
	committer := &GitIdentity{Name: id.Name, Email: id.Email}
	return author, committer
}
