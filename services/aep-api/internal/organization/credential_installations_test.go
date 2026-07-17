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

// UNIT + DBTEST tiers for the App-installation path (fetchInstallation,
// ListInstallationRepos/listInstallationRepos, fetchAppBotIdentity,
// ResolveUserInstallations).
//
// fetchInstallation and fetchAppBotIdentity sign their own App JWT
// (secrets.AppTokenMinter.SignAppJWT) and hit s.githubAPI directly via
// s.httpClient — both are TEST-SEAMED via WithGitHubAPIBase, so their GitHub
// calls run for real against an httptest fake, no live network.
//
// listInstallationRepos (selected-repos fetch inside fetchInstallation, and
// the public ListInstallationRepos wrapper) first mints an installation
// token via AppTokenMinter.MintForInstallation, which POSTs to a HARDCODED
// https://api.github.com/... URL using the minter's own *http.Client — a
// private field of package credentials with no test seam reachable from
// here. Two consequences pinned below:
//   - With an UNCONFIGURED minter (no App key), MintForInstallation returns
//     secrets.ErrAppNotConfigured before any network I/O — fully
//     deterministic, no network, and exercised directly.
//   - With a CONFIGURED minter (needed for fetchInstallation's own JWT to
//     succeed), the token-mint call cannot be redirected to a fake without
//     either reaching package credentials or touching the real internet. The
//     happy-path test below forces that specific hop to fail deterministically
//     via context cancellation (cancelAfterResponseTransport) instead — this
//     pins fetchInstallation's real "best effort" fallback (log + continue
//     with an empty selectedRepos) without ever dialing api.github.com. The
//     multi-page repository-listing merge logic inside listInstallationRepos
//     itself (the part downstream of a successful mint) is NOT reachable from
//     this package for the same reason and is left uncovered here.
//
// ResolveUserInstallations talks to GitHub exclusively through the
// sourcecontrol.AppInstallOps port, so it is faked at that edge like any other
// out-of-process dependency — no HTTP or minter trickery needed.
package organization

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/repositories"
)

// --- key + minter helpers -----------------------------------------------------

// genTestRSAKeyPEM generates a fresh 2048-bit RSA key for App-JWT signing.
// Mirrors internal/credentials/app_token_minter_test.go's generateTestKey,
// duplicated here because that helper is unexported in a different package.
func genTestRSAKeyPEM(t testing.TB) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

// newConfiguredMinter builds an AppTokenMinter with real key material so
// SignAppJWT succeeds — needed for fetchInstallation/fetchAppBotIdentity's
// own JWT-signed calls.
func newConfiguredMinter(t testing.TB, appID int64) *secrets.AppTokenMinter {
	t.Helper()
	m, err := secrets.NewAppTokenMinter(&secrets.AppKeyMaterial{AppID: appID, PrivateKeyPEM: genTestRSAKeyPEM(t)})
	if err != nil {
		t.Fatalf("NewAppTokenMinter: %v", err)
	}
	return m
}

// newUnconfiguredMinter builds a minter with no App key material — every
// SignAppJWT/MintForInstallation call fails fast with ErrAppNotConfigured,
// deterministically and without any network I/O.
func newUnconfiguredMinter(t testing.TB) *secrets.AppTokenMinter {
	t.Helper()
	m, err := secrets.NewAppTokenMinter(nil)
	if err != nil {
		t.Fatalf("NewAppTokenMinter(nil): %v", err)
	}
	return m
}

// installSvc builds a CredentialService for the App-installation tests: db
// and store stay nil (none of fetchInstallation/fetchAppBotIdentity/
// listInstallationRepos touch either), minter + GitHub base are the only
// wiring under test.
func installSvc(t testing.TB, minter *secrets.AppTokenMinter, ghBase string) *CredentialService {
	t.Helper()
	return NewCredentialService(nil, nil, minter, "", "", "", nil).WithGitHubAPIBase(ghBase)
}

// --- fetchInstallation: pre-mint branches (no network trickery needed) ------

func TestFetchInstallation_ErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured minter fails signing before any HTTP call", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t) // no routes registered — a hit here would 404, proving over-reach
		svc := installSvc(t, newUnconfiguredMinter(t), gh.URL)
		_, _, _, err := svc.fetchInstallation(context.Background(), 1)
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("want *ConflictError (app sign failure), got %#v", err)
		}
		if !strings.Contains(ce.Reason, "app sign") {
			t.Fatalf("ConflictError.Reason = %q; want it to mention app sign", ce.Reason)
		}
	})

	t.Run("404 → installation_not_found", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app/installations/42", 404, `{"message":"Not Found"}`)
		svc := installSvc(t, newConfiguredMinter(t, 111), gh.URL)
		_, _, _, err := svc.fetchInstallation(context.Background(), 42)
		assertValidationCode(t, err, "installation_not_found")
	})

	t.Run("non-200/404 → github_error", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app/installations/42", 500, `boom`)
		svc := installSvc(t, newConfiguredMinter(t, 111), gh.URL)
		_, _, _, err := svc.fetchInstallation(context.Background(), 42)
		assertValidationCode(t, err, "github_error")
	})

	t.Run("malformed JSON body → decode error", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app/installations/42", 200, `{not json`)
		svc := installSvc(t, newConfiguredMinter(t, 111), gh.URL)
		_, _, _, err := svc.fetchInstallation(context.Background(), 42)
		if err == nil {
			t.Fatalf("want a decode error for malformed JSON")
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("malformed JSON should be a raw decode error, not a *ValidationError: %v", err)
		}
	})

	t.Run("200 but missing account.login → install_no_account", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app/installations/42", 200, `{"account":{"type":"Organization"}}`)
		svc := installSvc(t, newConfiguredMinter(t, 111), gh.URL)
		_, _, _, err := svc.fetchInstallation(context.Background(), 42)
		assertValidationCode(t, err, "install_no_account")
	})
}

// --- fetchInstallation: happy path (forces the unseamed mint hop to fail) ---

// cancelAfterResponseTransport lets a test force fetchInstallation's SECOND,
// unseamed HTTP hop (the installation-token mint, hardcoded inside
// secrets.AppTokenMinter to real api.github.com with no test seam
// reachable from this package) to fail deterministically without touching
// the network. It fully buffers the FIRST response — so the caller's
// io.ReadAll is unaffected by what happens next — then cancels ctx. Because
// fetchInstallation issues its second request with that same ctx immediately
// afterward, in the same goroutine, strictly after this RoundTrip returns,
// the mint's http.NewRequestWithContext sees an already-Done context and
// fails before dialing: deterministic regardless of whether outbound network
// is actually reachable where the test runs.
type cancelAfterResponseTransport struct {
	inner  http.RoundTripper
	cancel context.CancelFunc
	fired  bool
}

func (rt *cancelAfterResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.inner.RoundTrip(req)
	if err == nil && resp.Body != nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	if !rt.fired {
		rt.fired = true
		rt.cancel()
	}
	return resp, err
}

func TestFetchInstallation_HappyPath_SelectedReposBestEffortFallback(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gh := newStubGitHub(t)
	gh.on("GET", "/app/installations/777", 200, `{"account":{"login":"acme-org","type":"Organization"},"repository_selection":"selected"}`)

	svc := installSvc(t, newConfiguredMinter(t, 555), gh.URL)
	svc.httpClient = &http.Client{Transport: &cancelAfterResponseTransport{inner: http.DefaultTransport, cancel: cancel}}

	login, acctType, repos, err := svc.fetchInstallation(ctx, 777)
	if err != nil {
		t.Fatalf("fetchInstallation: %v", err)
	}
	if login != "acme-org" || acctType != "Organization" {
		t.Fatalf("account fields: login=%q type=%q; want acme-org/Organization", login, acctType)
	}
	// Quirk: the account fields parse fine, but selectedRepos silently comes
	// back empty — listInstallationRepos' mint call failed (forced, here) and
	// fetchInstallation treats that as best-effort, per its doc comment.
	if len(repos) != 0 {
		t.Fatalf("selectedRepos = %v; want empty (best-effort fallback when the mint hop fails)", repos)
	}
}

// --- listInstallationRepos / ListInstallationRepos ---------------------------

func TestListInstallationRepos_UnconfiguredMinter_NoNetwork(t *testing.T) {
	t.Parallel()
	// privateKey == nil short-circuits MintForInstallation before any HTTP
	// work — deterministic, no network, and exercises listInstallationRepos'
	// error-propagation branch directly.
	gh := newStubGitHub(t) // no routes registered — any hit would 404
	svc := installSvc(t, newUnconfiguredMinter(t), gh.URL)
	repos, err := svc.ListInstallationRepos(context.Background(), 42)
	if !errors.Is(err, secrets.ErrAppNotConfigured) {
		t.Fatalf("ListInstallationRepos error = %v; want ErrAppNotConfigured", err)
	}
	if repos != nil {
		t.Fatalf("repos = %v; want nil on mint failure", repos)
	}
}

// --- fetchAppBotIdentity -------------------------------------------------------

func TestFetchAppBotIdentity(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured minter fails signing, no HTTP call", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t) // no routes registered
		svc := installSvc(t, newUnconfiguredMinter(t), gh.URL)
		_, err := svc.fetchAppBotIdentity(context.Background())
		if !errors.Is(err, secrets.ErrAppNotConfigured) {
			t.Fatalf("fetchAppBotIdentity error = %v; want ErrAppNotConfigured", err)
		}
	})

	t.Run("200 with slug+name builds the [bot] identity", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app", 200, `{"slug":"aep","name":"AEP Bot"}`)
		svc := installSvc(t, newConfiguredMinter(t, 222), gh.URL)
		id, err := svc.fetchAppBotIdentity(context.Background())
		if err != nil {
			t.Fatalf("fetchAppBotIdentity: %v", err)
		}
		want := secrets.Identity{Name: "AEP Bot", Email: "aep[bot]@users.noreply.github.com", Login: "aep[bot]"}
		if id != want {
			t.Fatalf("identity = %+v; want %+v", id, want)
		}
	})

	t.Run("200 missing slug is an error", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app", 200, `{"name":"AEP Bot"}`)
		svc := installSvc(t, newConfiguredMinter(t, 222), gh.URL)
		_, err := svc.fetchAppBotIdentity(context.Background())
		if err == nil || !strings.Contains(err.Error(), "missing slug") {
			t.Fatalf("want a missing-slug error, got %v", err)
		}
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app", 500, `boom`)
		svc := installSvc(t, newConfiguredMinter(t, 222), gh.URL)
		_, err := svc.fetchAppBotIdentity(context.Background())
		if err == nil || !strings.Contains(err.Error(), "GET /app:") {
			t.Fatalf("want a wrapped GET /app error, got %v", err)
		}
	})

	t.Run("malformed JSON is a decode error", func(t *testing.T) {
		t.Parallel()
		gh := newStubGitHub(t)
		gh.on("GET", "/app", 200, `{not json`)
		svc := installSvc(t, newConfiguredMinter(t, 222), gh.URL)
		_, err := svc.fetchAppBotIdentity(context.Background())
		if err == nil {
			t.Fatalf("want a decode error for malformed JSON")
		}
	})
}

// --- ResolveUserInstallations --------------------------------------------------

// fakeAppInstallOps hand-fakes sourcecontrol.AppInstallOps. ResolveUserInstallations
// only calls ExchangeOAuthCode, GetUserInstallations, and ListAppInstallations;
// the other two methods are not on its path, so a call to one is a test bug —
// they panic.
type fakeAppInstallOps struct {
	exchangeFn     func(ctx context.Context, clientID, clientSecret, code, redirectURI string) (string, error)
	userInstallsFn func(ctx context.Context, userToken string) ([]int64, error)
	listAppFn      func(ctx context.Context, minter *secrets.AppTokenMinter) ([]sourcecontrol.AppInstallationSummary, error)
}

var _ sourcecontrol.AppInstallOps = (*fakeAppInstallOps)(nil)

func (f *fakeAppInstallOps) GetUser(context.Context, secrets.Credential) (*sourcecontrol.GitHubUser, error) {
	panic("fakeAppInstallOps: GetUser is not on the ResolveUserInstallations path")
}

func (f *fakeAppInstallOps) GetAppInstallation(context.Context, *secrets.AppTokenMinter, int64) (*sourcecontrol.AppInstallationInfo, error) {
	panic("fakeAppInstallOps: GetAppInstallation is not on the ResolveUserInstallations path")
}

func (f *fakeAppInstallOps) ListAppInstallations(ctx context.Context, minter *secrets.AppTokenMinter) ([]sourcecontrol.AppInstallationSummary, error) {
	if f.listAppFn != nil {
		return f.listAppFn(ctx, minter)
	}
	return nil, nil
}

func (f *fakeAppInstallOps) ExchangeOAuthCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (string, error) {
	if f.exchangeFn != nil {
		return f.exchangeFn(ctx, clientID, clientSecret, code, redirectURI)
	}
	return "user-token", nil
}

func (f *fakeAppInstallOps) GetUserInstallations(ctx context.Context, userToken string) ([]int64, error) {
	if f.userInstallsFn != nil {
		return f.userInstallsFn(ctx, userToken)
	}
	return nil, nil
}

func (f *fakeAppInstallOps) DeleteInstallation(context.Context, *secrets.AppTokenMinter, int64) error {
	panic("fakeAppInstallOps: DeleteInstallation is not on the ResolveUserInstallations path")
}

func TestResolveUserInstallations_NotConfigured(t *testing.T) {
	t.Parallel()

	t.Run("nil githubClient → ErrAppBindNotConfigured", func(t *testing.T) {
		t.Parallel()
		svc := NewCredentialService(nil, nil, newConfiguredMinter(t, 1), "", "client-id", "client-secret", nil)
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if !errors.Is(err, ErrAppBindNotConfigured) {
			t.Fatalf("err = %v; want ErrAppBindNotConfigured", err)
		}
	})

	t.Run("unconfigured minter (AppID==0) → ErrAppBindNotConfigured", func(t *testing.T) {
		t.Parallel()
		svc := NewCredentialService(nil, nil, newUnconfiguredMinter(t), "", "client-id", "client-secret", &fakeAppInstallOps{})
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if !errors.Is(err, ErrAppBindNotConfigured) {
			t.Fatalf("err = %v; want ErrAppBindNotConfigured", err)
		}
	})

	t.Run("empty appClientID/appClientSecret → ErrAppBindNotConfigured", func(t *testing.T) {
		t.Parallel()
		svc := NewCredentialService(nil, nil, newConfiguredMinter(t, 1), "", "", "", &fakeAppInstallOps{})
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if !errors.Is(err, ErrAppBindNotConfigured) {
			t.Fatalf("err = %v; want ErrAppBindNotConfigured", err)
		}
	})
}

func TestResolveUserInstallations_ValidationAndUpstreamErrors(t *testing.T) {
	t.Parallel()
	minter := newConfiguredMinter(t, 1)

	t.Run("empty ocOrgID", func(t *testing.T) {
		t.Parallel()
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", &fakeAppInstallOps{})
		_, err := svc.ResolveUserInstallations(context.Background(), "", "code", "https://cb")
		assertValidationCode(t, err, "oc_org_id_missing")
	})

	t.Run("empty oauthCode", func(t *testing.T) {
		t.Parallel()
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", &fakeAppInstallOps{})
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "", "https://cb")
		assertValidationCode(t, err, "oauth_code_missing")
	})

	t.Run("ExchangeOAuthCode error → oauth_exchange_failed", func(t *testing.T) {
		t.Parallel()
		gh := &fakeAppInstallOps{exchangeFn: func(context.Context, string, string, string, string) (string, error) {
			return "", errors.New("bad code")
		}}
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", gh)
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		assertValidationCode(t, err, "oauth_exchange_failed")
	})

	t.Run("empty userToken from exchange → empty slice, nil error, no further calls", func(t *testing.T) {
		t.Parallel()
		gh := &fakeAppInstallOps{
			exchangeFn: func(context.Context, string, string, string, string) (string, error) { return "", nil },
			userInstallsFn: func(context.Context, string) ([]int64, error) {
				t.Fatalf("GetUserInstallations must not be called when the exchange yields no token")
				return nil, nil
			},
		}
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", gh)
		got, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got = %#v; want an empty, non-nil slice", got)
		}
	})

	t.Run("GetUserInstallations error propagates", func(t *testing.T) {
		t.Parallel()
		gh := &fakeAppInstallOps{userInstallsFn: func(context.Context, string) ([]int64, error) {
			return nil, errors.New("github: 500")
		}}
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", gh)
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if err == nil || !strings.Contains(err.Error(), "get user installations") {
			t.Fatalf("err = %v; want wrapped get-user-installations error", err)
		}
	})

	t.Run("ListAppInstallations error propagates", func(t *testing.T) {
		t.Parallel()
		gh := &fakeAppInstallOps{
			userInstallsFn: func(context.Context, string) ([]int64, error) { return []int64{1}, nil },
			listAppFn: func(context.Context, *secrets.AppTokenMinter) ([]sourcecontrol.AppInstallationSummary, error) {
				return nil, errors.New("github: 503")
			},
		}
		svc := NewCredentialService(nil, nil, minter, "", "cid", "csecret", gh)
		_, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
		if err == nil || !strings.Contains(err.Error(), "list app installations") {
			t.Fatalf("err = %v; want wrapped list-app-installations error", err)
		}
	})
}

// TestResolveUserInstallations_FiltersByUserAccessAndOrgBinding_DB pins the
// cross-tenant filter: only installations the user administers (per
// GetUserInstallations) AND that are either unbound or bound to the
// REQUESTING org survive; installs bound to a different org are dropped so
// their existence isn't leaked to an admin who happens to share GitHub
// access. This filter reads org_credentials directly, so it needs a real DB.
func TestResolveUserInstallations_FiltersByUserAccessAndOrgBinding_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)

	// installation 1: bound to "acme" (the requesting org) — kept.
	insertAppRow(t, db, "acme", 1, "active", nil)
	// installation 2: bound to "other-org" — must be dropped.
	insertAppRow(t, db, "other-org", 2, "active", nil)
	// installation 3: bound to a DIFFERENT other org but disconnected — status
	// filter (`IN ('active','suspended')`) means a disconnected row doesn't
	// count as "bound elsewhere", so this installation is NOT filtered out.
	insertAppRow(t, db, "other-org-2", 3, "disconnected", nil)
	// installation 4: no row at all — unbound, kept.
	// installation 5: the user does NOT administer this one on GitHub — must
	// be dropped regardless of binding.

	minter := newConfiguredMinter(t, 1)
	gh := &fakeAppInstallOps{
		userInstallsFn: func(context.Context, string) ([]int64, error) {
			return []int64{1, 2, 3, 4}, nil // user administers 1-4, not 5
		},
		listAppFn: func(context.Context, *secrets.AppTokenMinter) ([]sourcecontrol.AppInstallationSummary, error) {
			return []sourcecontrol.AppInstallationSummary{
				{InstallationID: 1, AccountLogin: "acme-org", AccountType: "Organization"},
				{InstallationID: 2, AccountLogin: "other-org", AccountType: "Organization"},
				{InstallationID: 3, AccountLogin: "other-org-disconnected", AccountType: "Organization"},
				{InstallationID: 4, AccountLogin: "fresh-org", AccountType: "Organization"},
				{InstallationID: 5, AccountLogin: "no-access-org", AccountType: "Organization"},
			}, nil
		},
	}
	svc := NewCredentialService(repositories.NewOrgCredentialRepository(db), nil, minter, "", "cid", "csecret", gh)

	got, err := svc.ResolveUserInstallations(context.Background(), "acme", "code", "https://cb")
	if err != nil {
		t.Fatalf("ResolveUserInstallations: %v", err)
	}
	ids := map[int64]bool{}
	for _, c := range got {
		ids[c.InstallationID] = true
	}
	if len(got) != 3 || !ids[1] || !ids[3] || !ids[4] {
		t.Fatalf("filtered candidates = %+v; want installations {1,3,4}", got)
	}
	if ids[2] {
		t.Fatalf("installation 2 (bound to another active org) must be filtered out: %+v", got)
	}
	if ids[5] {
		t.Fatalf("installation 5 (user has no GitHub access) must be filtered out: %+v", got)
	}
}
