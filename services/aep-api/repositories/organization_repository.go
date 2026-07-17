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

package repositories

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/aep/aep-api/models"
)

// OrganizationRepository manages the local `organizations` side-car rows (the
// UUID + thunder_org_uuid mapping the org domain keeps for an OC namespace).
// Every accessor is keyed by the org handle (`name`) — the identifier the BFF
// puts in OC URLs and every per-org lookup — never by a broadenable predicate,
// so a dropped filter is a missing method, not a cross-org write.
type OrganizationRepository interface {
	// ListByNames loads the rows whose name is in names (one indexed batch).
	ListByNames(ctx context.Context, names []string) ([]models.Organization, error)
	// GetByName returns the row for name, or nil when absent (not an error) —
	// the verify path treats absence as "fall through to OC".
	GetByName(ctx context.Context, name string) (*models.Organization, error)
	// Create inserts a row, returning the raw driver error so the caller can
	// classify a unique-name race with IsUniqueViolation.
	Create(ctx context.Context, org *models.Organization) error
	// SetThunderOrgUUID writes just the thunder_org_uuid column for name.
	SetThunderOrgUUID(ctx context.Context, name string, id uuid.UUID) error
}

type organizationRepository struct {
	db *gorm.DB
}

// NewOrganizationRepository constructs the gorm-backed OrganizationRepository.
func NewOrganizationRepository(db *gorm.DB) OrganizationRepository {
	return &organizationRepository{db: db}
}

func (r *organizationRepository) ListByNames(ctx context.Context, names []string) ([]models.Organization, error) {
	var rows []models.Organization
	if err := r.db.WithContext(ctx).Where("name IN ?", names).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *organizationRepository) GetByName(ctx context.Context, name string) (*models.Organization, error) {
	var row models.Organization
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *organizationRepository) Create(ctx context.Context, org *models.Organization) error {
	return r.db.WithContext(ctx).Create(org).Error
}

func (r *organizationRepository) SetThunderOrgUUID(ctx context.Context, name string, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&models.Organization{}).
		Where("name = ?", name).
		Update("thunder_org_uuid", id).Error
}

// IsUniqueViolation reports whether err is a unique-constraint conflict — the
// signal a create raced a concurrent insert of the same key. Recognises gorm's
// typed sentinel and falls back to the driver message (some paths surface the
// raw pq error rather than the translated one).
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
