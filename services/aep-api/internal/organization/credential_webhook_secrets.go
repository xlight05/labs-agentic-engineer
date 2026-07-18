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

// credential_webhook_secrets.go — per-org webhook HMAC secret list
// management (PAT mode): read, append-rotate, remove.

package organization

import (
	"context"
	"fmt"
	"time"
)

// ----------------------------------------------------------------------------
// Webhook secrets — GET / POST / DELETE
// ----------------------------------------------------------------------------

// GetWebhookSecrets returns the accepted HMAC keys for ocOrgID, current-first.
// PAT mode reads from the row's webhook_secrets JSONB. App mode reads from
// the platform-wide secret/aep/_platform/github/app/webhook_secret.
func (s *CredentialService) GetWebhookSecrets(ctx context.Context, ocOrgID string) ([][]byte, error) {
	row, err := s.fetchRow(ctx, ocOrgID)
	if err != nil {
		return nil, err
	}
	if row.Status != "active" {
		return nil, &ConflictError{Reason: fmt.Sprintf("org %s status=%s", ocOrgID, row.Status)}
	}

	switch row.Kind {
	case "user-pat":
		if len(row.WebhookSecrets) == 0 {
			return nil, &ConflictError{Reason: "no webhook secrets configured"}
		}
		out := make([][]byte, 0, len(row.WebhookSecrets))
		for _, e := range row.WebhookSecrets {
			out = append(out, []byte(e.Secret))
		}
		return out, nil

	case "app-installation":
		// Platform-wide secret list at _platform/github/app/webhook_secret.
		// Loaded via the platform-key path (which only the seed loads
		// directly — for the receiver-time read we go through a tiny
		// helper on AppTokenMinter that doesn't break the import fence).
		secrets, err := s.minter.LoadAppWebhookSecrets(ctx)
		if err != nil {
			return nil, fmt.Errorf("webhook secrets: load app secrets: %w", err)
		}
		if len(secrets) == 0 {
			return nil, &ConflictError{Reason: "no app webhook secrets configured"}
		}
		return secrets, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", row.Kind)
	}
}

// AppendWebhookSecret rotates a new secret onto the PAT row's list.
// 409 if called against an App-mode row (rotation lives in _platform).
func (s *CredentialService) AppendWebhookSecret(ctx context.Context, ocOrgID, secret string) error {
	if secret == "" {
		return &ValidationError{Code: "secret_empty", Message: "secret is required"}
	}
	return s.repo.Tx(ctx, func(tx OrgCredentialTx) error {
		if err := tx.AdvisoryLock("org:" + ocOrgID); err != nil {
			return err
		}
		row, err := tx.GetByOrg(ocOrgID)
		if err != nil {
			return err
		}
		if row == nil {
			return &NotFoundError{What: "org_credentials"}
		}
		if row.Kind != "user-pat" {
			return &ConflictError{Reason: "webhook-secret rotation is PAT-only; App-mode rotation lives in _platform"}
		}
		row.WebhookSecrets = append(WebhookSecrets{{Secret: secret, AddedAt: time.Now().UTC()}}, row.WebhookSecrets...)
		return tx.UpdateColumns(ocOrgID, map[string]any{"webhook_secrets": row.WebhookSecrets})
	})
}

// RemoveWebhookSecret drops a specific secret from the PAT row's list.
func (s *CredentialService) RemoveWebhookSecret(ctx context.Context, ocOrgID, secret string) error {
	return s.repo.Tx(ctx, func(tx OrgCredentialTx) error {
		if err := tx.AdvisoryLock("org:" + ocOrgID); err != nil {
			return err
		}
		row, err := tx.GetByOrg(ocOrgID)
		if err != nil {
			return err
		}
		if row == nil {
			return &NotFoundError{What: "org_credentials"}
		}
		if row.Kind != "user-pat" {
			return &ConflictError{Reason: "webhook-secret rotation is PAT-only"}
		}
		filtered := row.WebhookSecrets[:0]
		for _, e := range row.WebhookSecrets {
			if e.Secret != secret {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			return &ConflictError{Reason: "cannot drop the last webhook secret"}
		}
		row.WebhookSecrets = filtered
		return tx.UpdateColumns(ocOrgID, map[string]any{"webhook_secrets": row.WebhookSecrets})
	})
}
