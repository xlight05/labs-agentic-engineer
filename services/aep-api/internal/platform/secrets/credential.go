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

// Package secrets is the kernel module that owns every secret BACKEND, and the
// single seam for GitHub authentication (docs/design/domain-oriented-architecture.md
// §10.4).
//
// Secret storage is spread across four genuinely different backends — OpenBao
// (sealed git tokens), the SM-API (runner mirrors), Kubernetes ExternalSecrets
// (pods), and Postgres (the publisher secret). This module gives them ONE home
// and ONE arch fence: the OpenBao/Vault SDK may be imported from here and nowhere
// else (TestImportFences), so no business domain can reach a backend directly.
//
// It exposes a FEW purpose-specific ports, never one god port: the four backends
// serve different purposes, and one interface over them would be a false common
// core. The user-facing "connect my GitHub / Anthropic / resource secret"
// capabilities are NOT here — they stay in their business domains
// (organization, dependencies) and call these ports.
//
// Migration note: this was internal/credentials. The other three backends are
// routed through it in P3, not P0.
//
// # GitHub authentication
//
// Every code path that calls GitHub or runs `git` against a remote routes
// through Resolver.Resolve(ocOrgID) to obtain a Credential, then asks the
// credential for a token, identity, repo-owner, or webhook strategy as needed.
//
// Implementations cover App-installation and per-org user-PAT kinds; call
// sites stay identical because they consume the polymorphic Credential
// surface and never branch on the kind.
//
// Three architectural rules these types enforce:
//
//  1. No call site type-switches on Credential.
//  2. No call site reads identity, repo-owner, or token from any other
//     source — not env, not the GitRepository row.
//  3. Every external GitHub operation passes ocOrgID explicitly. Resolvers
//     refuse an empty ocOrgID.
package secrets

import (
	"context"
	"errors"
	"time"
)

// Credential is a polymorphic surface over the ways the platform can
// authenticate to GitHub (App-installation and per-org user-PAT).
//
// Callers MUST NOT type-switch on the implementation.
type Credential interface {
	// Token returns a usable GitHub token and the time at which it stops
	// being valid. Long-lived kinds may return time.Time{} (zero) to
	// indicate "never expires" — callers treat zero as "no refresh needed".
	Token(ctx context.Context) (token string, expiresAt time.Time, err error)

	// Identity returns the committer attribution this credential maps to.
	Identity() Identity

	// RepoOwner returns the GitHub org/user login under which new repos are
	// provisioned. App mode: the install's account login. PAT mode: the
	// GitHub org chosen at connect time.
	RepoOwner() string

	// WebhookStrategy says how the platform should arrange event delivery
	// for repos using this credential. Some kinds answer "register a
	// per-repo hook"; others answer "rely on platform-level delivery, do
	// nothing." Callers dispatch the strategy without inspecting which
	// kind it is.
	WebhookStrategy() WebhookStrategy
}

// Identity is the committer attribution surfaced by a Credential. The Login
// field is the GitHub user/bot login (used for hosts.yml + audit); Name and
// Email are what go on git commit author/committer headers.
type Identity struct {
	Name  string
	Email string
	Login string
}

// WebhookStrategy enumerates how the platform arranges event delivery for
// repos backed by a given Credential.
type WebhookStrategy int

const (
	// WebhookPerRepo says: register a webhook on each repo at provision time.
	// User-PAT mode uses this strategy.
	WebhookPerRepo WebhookStrategy = iota
	// WebhookPlatform says: event delivery is platform-wide (a GitHub App's
	// configured callback). App-installation mode uses this strategy.
	WebhookPlatform
)

// Resolver resolves the credential for a given organisation by looking up
// its per-org connection record. ocOrgID is MANDATORY — every external
// GitHub op names the org it acts for.
type Resolver interface {
	Resolve(ctx context.Context, ocOrgID string) (Credential, error)
}

// ErrEmptyOcOrgID is returned by resolvers when an empty ocOrgID is passed.
// This is the multi-tenant invariant — every external GitHub op names the
// org it acts for.
var ErrEmptyOcOrgID = errors.New("credentials: ocOrgID is required")
