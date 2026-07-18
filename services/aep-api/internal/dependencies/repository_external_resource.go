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

package dependencies

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/wso2/aep/aep-api/internal/spec"
)

// ErrExternalResourceNotFound is returned when no external resource is
// registered for the org under the given name.
var ErrExternalResourceNotFound = errors.New("external resource not found")

// ExternalResourceConsumer is one component that depends on a registered
// external resource.
type ExternalResourceConsumer struct {
	ProjectID     string `json:"projectId"`
	ComponentName string `json:"componentName"`
}

// ExternalResourceRepository is the org-level catalog of registered external
// resources — the reusable "definition" layer for external dependencies
// (name + description + config key schema + the OpenChoreo ResourceType the
// wiring maps to). Values/wiring are NOT stored here — they live per-project
// in the OC Resource model; the registry is the catalog the architect reads
// to reuse an external resource's shape across projects.
type ExternalResourceRepository struct {
	db *gorm.DB
}

// NewExternalResourceRepository returns a repository backed by db.
func NewExternalResourceRepository(db *gorm.DB) *ExternalResourceRepository {
	return &ExternalResourceRepository{db: db}
}

// Upsert registers (or updates) an external resource definition for an org.
// On first registration the OC ResourceType name equals the resource name. If
// the config key schema CHANGES for an existing resource, the ResourceType
// name gets a numeric suffix (salesforce → salesforce-2) — ResourceTypes are
// effectively immutable, so a new shape needs a new type — and the stored
// schema is updated. Description-only edits (schema unchanged, or an empty
// schema passed) don't bump the suffix.
func (r *ExternalResourceRepository) Upsert(ctx context.Context, orgID, name, description string, schema []spec.ConfigKey) (*ExternalResource, error) {
	if orgID == "" || name == "" {
		return nil, fmt.Errorf("external_resources: orgID and name are required")
	}
	var existing ExternalResource
	err := r.db.WithContext(ctx).Where("org_id = ? AND name = ?", orgID, name).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		res := &ExternalResource{
			OrgID:            orgID,
			Name:             name,
			Description:      description,
			ConfigKeys:       schema,
			ResourceTypeName: name,
		}
		if err := r.db.WithContext(ctx).Create(res).Error; err != nil {
			return nil, fmt.Errorf("external_resources: create %q: %w", name, err)
		}
		return res, nil
	case err != nil:
		return nil, fmt.Errorf("external_resources: lookup %q: %w", name, err)
	default:
		existing.Description = description
		if len(schema) > 0 && !SchemaEqual(existing.ConfigKeys, schema) {
			existing.ConfigKeys = schema
			existing.ResourceTypeName = bumpSuffix(existing.ResourceTypeName, name)
		}
		if err := r.db.WithContext(ctx).Save(&existing).Error; err != nil {
			return nil, fmt.Errorf("external_resources: update %q: %w", name, err)
		}
		return &existing, nil
	}
}

// Get returns an external resource by (org, name), or (nil, nil) when absent.
func (r *ExternalResourceRepository) Get(ctx context.Context, orgID, name string) (*ExternalResource, error) {
	var res ExternalResource
	err := r.db.WithContext(ctx).Where("org_id = ? AND name = ?", orgID, name).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("external_resources: get %q: %w", name, err)
	}
	return &res, nil
}

// List returns all external resources registered for an org, ordered by name.
func (r *ExternalResourceRepository) List(ctx context.Context, orgID string) ([]ExternalResource, error) {
	var out []ExternalResource
	if err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("name").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("external_resources: list org %q: %w", orgID, err)
	}
	return out, nil
}

// Delete removes an external resource's org-level registration. It does NOT
// check consumers — the caller (the delete endpoint) enforces the "not in
// use" guard so it can return a precise error. The shared, immutable OC
// ResourceType is intentionally left in place (re-registration reuses it).
func (r *ExternalResourceRepository) Delete(ctx context.Context, orgID, name string) error {
	if orgID == "" || name == "" {
		return fmt.Errorf("external_resources: orgID and name required")
	}
	res := r.db.WithContext(ctx).Where("org_id = ? AND name = ?", orgID, name).Delete(&ExternalResource{})
	if res.Error != nil {
		return fmt.Errorf("external_resources: delete %q: %w", name, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrExternalResourceNotFound
	}
	return nil
}

// SchemaEqual reports whether two config key schemas are equivalent (same
// keys, same secret flags), order-independent.
func SchemaEqual(a, b []spec.ConfigKey) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]bool, len(a))
	for _, k := range a {
		idx[k.Key] = k.Secret
	}
	for _, k := range b {
		s, ok := idx[k.Key]
		if !ok || s != k.Secret {
			return false
		}
	}
	return true
}

// bumpSuffix returns the next "-N" suffix for an immutable-ResourceType
// rename. base "salesforce", current "salesforce" → "salesforce-2";
// "salesforce-2" → "salesforce-3".
func bumpSuffix(current, base string) string {
	n := 1
	if rest, ok := strings.CutPrefix(current, base+"-"); ok {
		if parsed, err := strconv.Atoi(rest); err == nil {
			n = parsed
		}
	}
	return fmt.Sprintf("%s-%d", base, n+1)
}
