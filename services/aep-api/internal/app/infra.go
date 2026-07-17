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

package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k8sclient "github.com/wso2/aep/aep-api/internal/clients/k8s"
	"github.com/wso2/aep/aep-api/internal/config"
	"github.com/wso2/aep/aep-api/internal/migrate"
	"github.com/wso2/aep/aep-api/internal/platform/database"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/internal/seed"
)

// Infra is every external dependency Assemble needs already resolved — the
// results of all boot-time I/O. Resolve produces the real bundle (network, disk,
// OpenBao, the dev seed); Fake produces a zero-I/O bundle for assembly tests.
// Assemble reads it and does no I/O of its own, so the whole service graph
// assembles deterministically in milliseconds.
type Infra struct {
	DB              *gorm.DB
	CredStore       secrets.OpenBaoStore
	Minter          *secrets.AppTokenMinter
	AppClientSecret string        // GitHub App OAuth client_secret ("" ⇒ bind path disabled)
	K8sClient       client.Client // in-cluster client; nil ⇒ mint-build skips Secret writes
	Workspace       *gitfs.Engine
}

// Resolve performs every boot side effect and returns the resolved Infra: it
// opens the database and runs first-boot migrations (Bootstrap), builds the
// credential store, best-effort loads the GitHub App key / bot identity / OAuth
// client_secret from OpenBao (each with its own short timeout), runs the dev-only
// app-platform seed (fatal), initialises the in-cluster k8s client (optional),
// and fsck's the workspace root (fatal). This is the ONLY place in the graph that
// touches the network, the clock, OpenBao, or the filesystem at boot — Assemble
// is pure. Required infra errors; optional infra warns.
func Resolve(ctx context.Context, cfg config.Config) (Infra, error) {
	// Database + first-boot schema. Opened here (not in main) so main is just
	// Resolve → Assemble → serve, and Assemble never touches the DB at build time.
	db, err := database.Open(cfg.DatabaseURL, migrate.BaseModels()...)
	if err != nil {
		return Infra{}, fmt.Errorf("database init: %w", err)
	}
	if err := Bootstrap(ctx, db, cfg); err != nil {
		return Infra{}, err
	}

	// Credential store (AES-256-GCM over Postgres). Pure to construct, but the
	// OpenBao loads + dev seed below depend on it, so it is resolved here.
	credKey, err := base64.StdEncoding.DecodeString(cfg.CredentialEncryptionKey)
	if err != nil || len(credKey) != 32 {
		// config.Validate guarantees this decodes to 32 bytes; kept as defense.
		return Infra{}, fmt.Errorf("CREDENTIAL_ENCRYPTION_KEY must be a base64-encoded 32-byte key: %w", err)
	}
	credStore, err := secrets.NewDBStore(db, credKey)
	if err != nil {
		return Infra{}, fmt.Errorf("credential store init: %w", err)
	}
	slog.Info("credential store: postgres (aes-256-gcm)")

	// In-cluster k8s client (optional — mint-build skips Secret writes when nil).
	wpClient, err := k8sclient.NewInClusterClient()
	if err != nil {
		slog.Warn("k8s client init failed — mint-build will skip Secret writes; builds will fail at clone", "error", err)
		wpClient = nil
	}

	// App-token minter — best-effort App-key load. With no App key the minter
	// answers in no-app mode; the connect surface lights up the App path lazily on
	// first use.
	loadCtx, cancelLoad := context.WithTimeout(ctx, 10*time.Second)
	appKey, err := secrets.LoadAppKeyFromOpenBao(loadCtx, credStore)
	cancelLoad()
	if err != nil {
		slog.Warn("app key load failed; App-mode credentials will return ErrAppNotConfigured", "error", err)
		appKey = nil
	}
	minter, err := secrets.NewAppTokenMinter(appKey)
	if err != nil {
		return Infra{}, fmt.Errorf("app token minter init: %w", err)
	}
	minter.WithOpenBao(credStore)

	// Dev-only app-platform seed (App private key + client_secret + webhook HMAC).
	// No-op outside DEPLOYMENT_TIER=dev.
	{
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := seed.AppPlatformFromEnv(c, credStore, cfg); err != nil {
			cancel()
			return Infra{}, fmt.Errorf("app platform seed: %w", err)
		}
		cancel()
	}
	if appKey == nil {
		retryCtx, cancelRetry := context.WithTimeout(ctx, 10*time.Second)
		if reloaded, rerr := secrets.LoadAppKeyFromOpenBao(retryCtx, credStore); rerr == nil && reloaded != nil {
			cancelRetry()
			minter, err = secrets.NewAppTokenMinter(reloaded)
			if err != nil {
				return Infra{}, fmt.Errorf("app token minter re-init: %w", err)
			}
			minter.WithOpenBao(credStore)
			slog.Info("github app loaded post-seed", "appId", reloaded.AppID)
		} else {
			cancelRetry()
		}
	}
	if minter.AppID() != 0 {
		idCtx, cancelID := context.WithTimeout(ctx, 10*time.Second)
		if err := minter.LoadAppBotIdentity(idCtx, "https://api.github.com"); err != nil {
			slog.Warn("app bot identity load failed; will retry on first connect", "error", err)
		}
		cancelID()
	}
	var appClientSecret string
	if minter.AppID() != 0 {
		csCtx, cancelCS := context.WithTimeout(ctx, 10*time.Second)
		if cs, err := minter.LoadAppClientSecret(csCtx); err != nil {
			slog.Warn("app oauth client_secret load failed; bind path disabled", "error", err)
		} else {
			appClientSecret = cs
		}
		cancelCS()
	}

	// Workspace engine — the disk-backed git plumbing over the shared /workspaces
	// mount. Fail fast on an unusable root: the volume is mounted in compose/k8s,
	// and dev runs override AEP_WORKSPACE_ROOT.
	workspaceEngine, err := gitfs.New(cfg.Workspace.Root)
	if err != nil {
		return Infra{}, fmt.Errorf("workspace engine init (root %q): %w", cfg.Workspace.Root, err)
	}
	slog.Info("workspace engine", "root", workspaceEngine.Root())

	return Infra{
		DB:              db,
		CredStore:       credStore,
		Minter:          minter,
		AppClientSecret: appClientSecret,
		K8sClient:       wpClient,
		Workspace:       workspaceEngine,
	}, nil
}

// Fake returns a zero-I/O Infra for assembly tests. The DB is a non-nil but
// UNCONNECTED *gorm.DB: the graph's constructors only store it (and some assert
// it is non-nil, e.g. JobWatcher) — nothing queries it at assembly time, so no
// connection is opened. The workspace is nil for the same reason (constructors
// store the pointer; the trash hooks / reaper only deref it when invoked). The
// minter (no-app mode) and credential store (cipher-only over the nil DB) are
// both pure to construct. app.Assemble(cfg, app.Fake()) thus builds the same real
// handler + watchers as production without touching the network, clock, or disk.
func Fake() Infra {
	minter, _ := secrets.NewAppTokenMinter(nil)               // no-app mode, no I/O
	credStore, _ := secrets.NewDBStore(nil, make([]byte, 32)) // AES cipher only, nil DB
	return Infra{
		DB:              &gorm.DB{},
		CredStore:       credStore,
		Minter:          minter,
		AppClientSecret: "",
		K8sClient:       nil,
		Workspace:       nil,
	}
}
