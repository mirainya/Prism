package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxPublicModelIdentityRenames = 32

// PublicModelIdentityRename changes a model's public identity and presentation.
// Description is optional so existing identity-only callers keep their text.
type PublicModelIdentityRename struct {
	FromAPIName string  `json:"from_api_name"`
	ToAPIName   string  `json:"to_api_name"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description,omitempty"`
}

// PublicModelIdentityChange applies a set of public model identity changes to
// one active catalog version. A batch is used for source-wide branding changes
// so clients never observe a partly-renamed catalog.
type PublicModelIdentityChange struct {
	catalogChangeGuard
	ReasonCode string                      `json:"reason_code"`
	Renames    []PublicModelIdentityRename `json:"renames"`
}

func (in *PublicModelIdentityChange) Normalize() {
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if in.ReasonCode == "" {
		in.ReasonCode = "public_identity_rename"
	}
	for index := range in.Renames {
		in.Renames[index].FromAPIName = strings.TrimSpace(in.Renames[index].FromAPIName)
		in.Renames[index].ToAPIName = strings.TrimSpace(in.Renames[index].ToAPIName)
		in.Renames[index].DisplayName = strings.TrimSpace(in.Renames[index].DisplayName)
		if in.Renames[index].Description != nil {
			*in.Renames[index].Description = strings.TrimSpace(*in.Renames[index].Description)
		}
	}
}

func (in PublicModelIdentityChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if in.ExpectedConfigVersion == 0 ||
		!catalogIdentityPattern.MatchString(in.ReasonCode) || len(in.ReasonCode) > 128 ||
		len(in.Renames) == 0 || len(in.Renames) > maxPublicModelIdentityRenames {
		return ErrInvalidInput
	}
	fromNames := make(map[string]struct{}, len(in.Renames))
	toNames := make(map[string]struct{}, len(in.Renames))
	for _, rename := range in.Renames {
		if !validPublicModelIdentity(rename.FromAPIName) ||
			!validPublicModelIdentity(rename.ToAPIName) ||
			!validDisplayName(rename.DisplayName) ||
			rename.Description != nil && (!utf8.ValidString(*rename.Description) || utf8.RuneCountInString(*rename.Description) > 1000) {
			return ErrInvalidInput
		}
		if _, duplicate := fromNames[rename.FromAPIName]; duplicate {
			return ErrInvalidInput
		}
		if _, duplicate := toNames[rename.ToAPIName]; duplicate {
			return ErrInvalidInput
		}
		fromNames[rename.FromAPIName] = struct{}{}
		toNames[rename.ToAPIName] = struct{}{}
	}
	return nil
}

type publicModelIdentityPlan struct {
	CatalogModelID uint64
	ModelID        uint64
	ModelNameID    uint64
	OldAPIName     string
	NewAPIName     string
	OldModelCode   string
	NewModelCode   string
	OldDisplayName string
	NewDisplayName string
	OldDescription string
	NewDescription *string
	SKUs           []publicSKUIdentityRename
}

type publicSKUIdentityRename struct {
	ID      uint64
	OldCode string
	NewCode string
}

// ChangePublicModelIdentities applies every change under the same active catalog
// lock and advances the catalog exactly once. Internal SKU codes are renamed
// only when the new public name is also a valid SKU-code prefix.
func (s *Store) ChangePublicModelIdentities(ctx context.Context, tx *sql.Tx, in PublicModelIdentityChange, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || actorID == 0 {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}

	plans := make([]publicModelIdentityPlan, 0, len(in.Renames))
	seenModels := make(map[uint64]struct{}, len(in.Renames))
	seenTargetSKUs := make(map[string]struct{})
	for _, rename := range in.Renames {
		plan, err := loadPublicModelIdentityPlan(ctx, tx, active.ID, rename)
		if err != nil {
			return CatalogChangeResult{}, err
		}
		if _, duplicate := seenModels[plan.CatalogModelID]; duplicate {
			return CatalogChangeResult{}, fmt.Errorf("%w: model is present more than once in the rename batch", ErrConflict)
		}
		seenModels[plan.CatalogModelID] = struct{}{}
		for _, sku := range plan.SKUs {
			if _, duplicate := seenTargetSKUs[sku.NewCode]; duplicate {
				return CatalogChangeResult{}, fmt.Errorf("%w: SKU target %q is present more than once in the rename batch", ErrConflict, sku.NewCode)
			}
			seenTargetSKUs[sku.NewCode] = struct{}{}
		}
		plans = append(plans, plan)
	}

	auditChanges := make([]ginSafeMetadata, 0, len(plans))
	for index := range plans {
		plan := &plans[index]
		if plan.OldAPIName != plan.NewAPIName {
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_model_names SET api_name=? WHERE id=? AND model_id=? AND api_name=?`, plan.NewAPIName, plan.ModelNameID, plan.ModelID, plan.OldAPIName)); err != nil {
				return CatalogChangeResult{}, err
			}
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_models SET model_code=? WHERE id=? AND model_code=?`, plan.NewModelCode, plan.ModelID, plan.OldModelCode)); err != nil {
				return CatalogChangeResult{}, err
			}
		}
		oldSKUCodes := make([]string, 0, len(plan.SKUs))
		newSKUCodes := make([]string, 0, len(plan.SKUs))
		for _, sku := range plan.SKUs {
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_skus SET sku_code=? WHERE release_id=? AND id=? AND sku_code=?`, sku.NewCode, active.ID, sku.ID, sku.OldCode)); err != nil {
				return CatalogChangeResult{}, err
			}
			oldSKUCodes = append(oldSKUCodes, sku.OldCode)
			newSKUCodes = append(newSKUCodes, sku.NewCode)
		}
		if plan.NewDescription == nil {
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_catalog_models SET display_name=? WHERE release_id=? AND id=?`, plan.NewDisplayName, active.ID, plan.CatalogModelID)); err != nil {
				return CatalogChangeResult{}, err
			}
		} else if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_catalog_models SET display_name=?,description=? WHERE release_id=? AND id=?`, plan.NewDisplayName, *plan.NewDescription, active.ID, plan.CatalogModelID)); err != nil {
			return CatalogChangeResult{}, err
		}
		change := ginSafeMetadata{
			"catalog_model_id": plan.CatalogModelID,
			"model_id":         plan.ModelID,
			"before": ginSafeMetadata{
				"api_name": plan.OldAPIName, "model_code": plan.OldModelCode,
				"display_name": plan.OldDisplayName, "sku_codes": oldSKUCodes,
			},
			"after": ginSafeMetadata{
				"api_name": plan.NewAPIName, "model_code": plan.NewModelCode,
				"display_name": plan.NewDisplayName, "sku_codes": newSKUCodes,
			},
		}
		if plan.NewDescription != nil {
			change["before"].(ginSafeMetadata)["description"] = plan.OldDescription
			change["after"].(ginSafeMetadata)["description"] = *plan.NewDescription
		}
		auditChanges = append(auditChanges, change)
	}

	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.public_model_identities", actorID, ginSafeMetadata{
		"source_release_id": active.ID,
		"reason_code":       in.ReasonCode,
		"change_count":      len(auditChanges),
		"changes":           auditChanges,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

func loadPublicModelIdentityPlan(ctx context.Context, tx *sql.Tx, releaseID uint64, rename PublicModelIdentityRename) (publicModelIdentityPlan, error) {
	plan := publicModelIdentityPlan{
		OldAPIName: rename.FromAPIName, NewAPIName: rename.ToAPIName,
		NewModelCode: rename.ToAPIName, NewDisplayName: rename.DisplayName,
		NewDescription: rename.Description,
	}
	err := tx.QueryRowContext(ctx, `SELECT cm.id,cm.model_id,mn.id,m.model_code,cm.display_name
FROM gw_catalog_models cm
JOIN gw_models m ON m.id=cm.model_id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id AND cmn.model_id=cm.model_id AND cmn.is_primary=TRUE
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
WHERE cm.release_id=? AND mn.api_name=? FOR UPDATE`, releaseID, rename.FromAPIName).
		Scan(&plan.CatalogModelID, &plan.ModelID, &plan.ModelNameID, &plan.OldModelCode, &plan.OldDisplayName)
	if err == sql.ErrNoRows {
		return publicModelIdentityPlan{}, ErrNotFound
	}
	if err != nil {
		return publicModelIdentityPlan{}, err
	}

	if plan.OldModelCode != rename.FromAPIName {
		return publicModelIdentityPlan{}, fmt.Errorf("%w: API name and model code do not share the requested source identity", ErrConflict)
	}
	if rename.Description != nil {
		if err := tx.QueryRowContext(ctx, `SELECT description FROM gw_catalog_models WHERE release_id=? AND id=? FOR UPDATE`, releaseID, plan.CatalogModelID).Scan(&plan.OldDescription); err != nil {
			return publicModelIdentityPlan{}, err
		}
	}
	if rename.FromAPIName == rename.ToAPIName {
		return plan, nil
	}
	var conflictID uint64
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_model_names WHERE api_name=? FOR UPDATE`, rename.ToAPIName).Scan(&conflictID)
	if err == nil {
		return publicModelIdentityPlan{}, fmt.Errorf("%w: API name %q already exists", ErrConflict, rename.ToAPIName)
	}
	if err != sql.ErrNoRows {
		return publicModelIdentityPlan{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_models WHERE model_code=? FOR UPDATE`, rename.ToAPIName).Scan(&conflictID)
	if err == nil {
		return publicModelIdentityPlan{}, fmt.Errorf("%w: model code %q already exists", ErrConflict, rename.ToAPIName)
	}
	if err != sql.ErrNoRows {
		return publicModelIdentityPlan{}, err
	}
	// Internal SKU codes retain their ASCII identity when the public upstream
	// model name contains characters that are not valid SKU-code characters.
	if !catalogIdentityPattern.MatchString(rename.ToAPIName) {
		return plan, nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.sku_code
FROM gw_skus s
JOIN gw_model_operations mo ON mo.release_id=s.release_id AND mo.id=s.model_operation_id
WHERE s.release_id=? AND mo.catalog_model_id=? ORDER BY s.id FOR UPDATE`, releaseID, plan.CatalogModelID)
	if err != nil {
		return publicModelIdentityPlan{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var sku publicSKUIdentityRename
		if err := rows.Scan(&sku.ID, &sku.OldCode); err != nil {
			return publicModelIdentityPlan{}, err
		}
		if !strings.HasPrefix(sku.OldCode, rename.FromAPIName) {
			continue
		}
		sku.NewCode = rename.ToAPIName + strings.TrimPrefix(sku.OldCode, rename.FromAPIName)
		if !catalogIdentityPattern.MatchString(sku.NewCode) {
			return publicModelIdentityPlan{}, ErrInvalidInput
		}
		plan.SKUs = append(plan.SKUs, sku)
	}
	if err := rows.Err(); err != nil {
		return publicModelIdentityPlan{}, err
	}
	if err := rows.Close(); err != nil {
		return publicModelIdentityPlan{}, err
	}
	for _, sku := range plan.SKUs {
		err := tx.QueryRowContext(ctx, `SELECT id FROM gw_skus WHERE release_id=? AND sku_code=? FOR UPDATE`, releaseID, sku.NewCode).Scan(&conflictID)
		if err == nil {
			return publicModelIdentityPlan{}, fmt.Errorf("%w: SKU code %q already exists", ErrConflict, sku.NewCode)
		}
		if err != sql.ErrNoRows {
			return publicModelIdentityPlan{}, err
		}
	}
	return plan, nil
}
