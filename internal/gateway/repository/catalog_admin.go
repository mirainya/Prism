package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	catalogIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	semanticVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

type CatalogDraftInput struct {
	SemanticVersion string `json:"semantic_version"`
	SemanticDigest  string `json:"-"`
}

func (in CatalogDraftInput) Validate() error {
	if !semanticVersionPattern.MatchString(in.SemanticVersion) || !validHexDigest(in.SemanticDigest, 32) {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) CreateCatalogDraft(ctx context.Context, tx *sql.Tx, in CatalogDraftInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 || in.Validate() != nil {
		return 0, ErrInvalidInput
	}
	var releaseNo uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(release_no),0)+1 FROM gw_catalog_releases`).Scan(&releaseNo); err != nil {
		return 0, err
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return 0, err
	}
	draftHash := sha256.Sum256(append([]byte(fmt.Sprintf("draft:%d:", releaseNo)), seed...))
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_releases(release_no,status,config_version,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at,updated_at) VALUES (?,'draft',1,1,'sha256',?,?,?,?,?)`, releaseNo, hex.EncodeToString(draftHash[:]), in.SemanticVersion, in.SemanticDigest, now, now)
	if err != nil {
		return 0, err
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_release_state_events(release_id,old_state,new_state,reason_code,created_at) VALUES (?,NULL,'draft','admin_create',?)`, id, now); err != nil {
		return 0, err
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog.create", "catalog_release", id, ginSafeMetadata{
		"release_no": releaseNo, "semantic_version": in.SemanticVersion,
	})
}

type ginSafeMetadata map[string]any

type CatalogSKUInput struct {
	ExpectedVersion      uint64   `json:"expected_version"`
	ModelCode            string   `json:"model_code"`
	APIName              string   `json:"api_name"`
	DisplayName          string   `json:"display_name"`
	Description          string   `json:"description"`
	Visibility           string   `json:"visibility"`
	CapabilityTags       []string `json:"capability_tags"`
	OperationCode        string   `json:"operation_code"`
	ContractVersion      uint32   `json:"contract_version"`
	HTTPMethod           string   `json:"http_method"`
	RouteTemplate        string   `json:"route_template"`
	NormalizationVersion uint32   `json:"normalization_version"`
	SKUCode              string   `json:"sku_code"`
	DeliveryMode         string   `json:"delivery_mode"`
	MaxResults           uint32   `json:"max_results"`
	IdempotencyMode      string   `json:"idempotency_mode"`
	ServiceTiers         []string `json:"service_tiers"`
}

func (in *CatalogSKUInput) Normalize() {
	in.ModelCode = strings.TrimSpace(in.ModelCode)
	in.APIName = strings.TrimSpace(in.APIName)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Description = strings.TrimSpace(in.Description)
	in.Visibility = strings.ToLower(strings.TrimSpace(in.Visibility))
	in.OperationCode = strings.ToLower(strings.TrimSpace(in.OperationCode))
	in.HTTPMethod = strings.ToUpper(strings.TrimSpace(in.HTTPMethod))
	in.RouteTemplate = strings.TrimSpace(in.RouteTemplate)
	in.SKUCode = strings.ToLower(strings.TrimSpace(in.SKUCode))
	in.DeliveryMode = strings.ToLower(strings.TrimSpace(in.DeliveryMode))
	in.IdempotencyMode = strings.ToLower(strings.TrimSpace(in.IdempotencyMode))
	for index := range in.CapabilityTags {
		in.CapabilityTags[index] = strings.ToLower(strings.TrimSpace(in.CapabilityTags[index]))
	}
	for index := range in.ServiceTiers {
		in.ServiceTiers[index] = strings.ToLower(strings.TrimSpace(in.ServiceTiers[index]))
	}
	sort.Strings(in.CapabilityTags)
	sort.Strings(in.ServiceTiers)
}

func (in CatalogSKUInput) Validate() error {
	if in.ExpectedVersion == 0 || !catalogIdentityPattern.MatchString(in.ModelCode) || !catalogIdentityPattern.MatchString(in.APIName) ||
		!validDisplayName(in.DisplayName) || !utf8.ValidString(in.Description) || utf8.RuneCountInString(in.Description) > 1000 ||
		(in.Visibility != "visible" && in.Visibility != "deprecated" && in.Visibility != "hidden") ||
		!catalogIdentityPattern.MatchString(in.OperationCode) || in.ContractVersion == 0 || in.NormalizationVersion == 0 ||
		(in.HTTPMethod != "GET" && in.HTTPMethod != "POST" && in.HTTPMethod != "DELETE") || !validRouteTemplate(in.RouteTemplate) ||
		!catalogIdentityPattern.MatchString(in.SKUCode) || (in.DeliveryMode != "reference" && in.DeliveryMode != "managed_copy") ||
		in.MaxResults == 0 || in.MaxResults > 64 || (in.IdempotencyMode != "required" && in.IdempotencyMode != "optional" && in.IdempotencyMode != "forbidden") ||
		!validStringSet(in.CapabilityTags, 32, 64) || !validStringSet(in.ServiceTiers, 16, 32) || len(in.ServiceTiers) == 0 {
		return ErrInvalidInput
	}
	return nil
}

func validRouteTemplate(value string) bool {
	return value != "" && len(value) <= 255 && strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "?#\x00\r\n")
}

func validStringSet(values []string, maxItems, maxLength int) bool {
	if len(values) > maxItems {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > maxLength || value != strings.TrimSpace(value) || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n\t") {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

type catalogDraftLock struct {
	Version        uint64
	ContentHash    string
	SemanticDigest string
}

func lockCatalogDraft(ctx context.Context, tx *sql.Tx, releaseID, expectedVersion uint64) (catalogDraftLock, error) {
	if tx == nil || releaseID == 0 || expectedVersion == 0 {
		return catalogDraftLock{}, ErrInvalidInput
	}
	var status string
	var out catalogDraftLock
	if err := tx.QueryRowContext(ctx, `SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`, releaseID).Scan(&status, &out.Version, &out.ContentHash, &out.SemanticDigest); err == sql.ErrNoRows {
		return catalogDraftLock{}, ErrNotFound
	} else if err != nil {
		return catalogDraftLock{}, err
	}
	if status != "draft" || out.Version != expectedVersion {
		return catalogDraftLock{}, ErrConflict
	}
	return out, nil
}

func advanceCatalogDraft(ctx context.Context, tx *sql.Tx, releaseID uint64, lock catalogDraftLock, action string, resourceID, actorID uint64, metadata any) error {
	next := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%d", lock.ContentHash, lock.Version+1, action, resourceID)))
	result, err := tx.ExecContext(ctx, `UPDATE gw_catalog_releases SET config_version=config_version+1,content_hash=?,updated_at=? WHERE id=? AND status='draft' AND config_version=?`, hex.EncodeToString(next[:]), nowUTC(), releaseID, lock.Version)
	if err != nil {
		return err
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return recordCatalogAdminChange(ctx, tx, actorID, action, "catalog_release", releaseID, metadata)
}

func (s *Store) CreateCatalogSKU(ctx context.Context, tx *sql.Tx, releaseID uint64, in CatalogSKUInput, actorID uint64) (uint64, error) {
	if tx == nil || releaseID == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return 0, err
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, in.ExpectedVersion)
	if err != nil {
		return 0, err
	}
	modelID, modelNameID, err := ensureCatalogModelIdentity(ctx, tx, in.ModelCode, in.APIName)
	if err != nil {
		return 0, err
	}
	catalogModelID, err := ensureCatalogModel(ctx, tx, releaseID, modelID, modelNameID, in)
	if err != nil {
		return 0, err
	}
	contractID, err := ensureOperationContract(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	operationID, err := ensureCatalogModelOperation(ctx, tx, releaseID, catalogModelID, contractID, lock.SemanticDigest, in.NormalizationVersion)
	if err != nil {
		return 0, err
	}
	tiers, _ := json.Marshal(in.ServiceTiers)
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_skus(release_id,model_operation_id,sku_code,delivery_mode,max_results,idempotency_mode,service_tiers,created_at) VALUES (?,?,?,?,?,?,?,?)`, releaseID, operationID, in.SKUCode, in.DeliveryMode, in.MaxResults, in.IdempotencyMode, tiers, nowUTC())
	if err != nil {
		return 0, err
	}
	skuID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	return skuID, advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog.sku.create", skuID, actorID, ginSafeMetadata{"sku_code": in.SKUCode, "model_code": in.ModelCode, "operation_code": in.OperationCode})
}

func ensureCatalogModelIdentity(ctx context.Context, tx *sql.Tx, modelCode, apiName string) (uint64, uint64, error) {
	var modelID uint64
	err := tx.QueryRowContext(ctx, `SELECT id FROM gw_models WHERE model_code=?`, modelCode).Scan(&modelID)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_models(model_code,created_at) VALUES (?,?)`, modelCode, nowUTC())
		if insertErr != nil {
			return 0, 0, insertErr
		}
		modelID, insertErr = lastID(result)
		if insertErr != nil {
			return 0, 0, insertErr
		}
	} else if err != nil {
		return 0, 0, err
	}
	var nameID, ownerID uint64
	err = tx.QueryRowContext(ctx, `SELECT id,model_id FROM gw_model_names WHERE api_name=?`, apiName).Scan(&nameID, &ownerID)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_model_names(model_id,api_name,is_primary,created_at) VALUES (?,?,TRUE,?)`, modelID, apiName, nowUTC())
		if insertErr != nil {
			return 0, 0, insertErr
		}
		nameID, insertErr = lastID(result)
		return modelID, nameID, insertErr
	}
	if err != nil {
		return 0, 0, err
	}
	if ownerID != modelID {
		return 0, 0, ErrConflict
	}
	return modelID, nameID, nil
}

func ensureCatalogModel(ctx context.Context, tx *sql.Tx, releaseID, modelID, modelNameID uint64, in CatalogSKUInput) (uint64, error) {
	var catalogModelID uint64
	var displayName, description, visibility string
	var rawTags []byte
	err := tx.QueryRowContext(ctx, `SELECT id,display_name,description,capability_tags,visibility FROM gw_catalog_models WHERE release_id=? AND model_id=?`, releaseID, modelID).
		Scan(&catalogModelID, &displayName, &description, &rawTags, &visibility)
	if err == sql.ErrNoRows {
		tags, _ := json.Marshal(in.CapabilityTags)
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_catalog_models(release_id,model_id,display_name,description,capability_tags,sort_order,visibility,created_at) VALUES (?,?,?,?,?,0,?,?)`, releaseID, modelID, in.DisplayName, in.Description, tags, in.Visibility, nowUTC())
		if insertErr != nil {
			return 0, insertErr
		}
		catalogModelID, insertErr = lastID(result)
		if insertErr != nil {
			return 0, insertErr
		}
	} else if err != nil {
		return 0, err
	} else {
		var existingTags []string
		if json.Unmarshal(rawTags, &existingTags) != nil || displayName != in.DisplayName || description != in.Description || visibility != in.Visibility || !slices.Equal(existingTags, in.CapabilityTags) {
			return 0, ErrConflict
		}
	}

	var existingCatalogModelID uint64
	err = tx.QueryRowContext(ctx, `SELECT catalog_model_id FROM gw_catalog_model_names WHERE release_id=? AND model_name_id=?`, releaseID, modelNameID).Scan(&existingCatalogModelID)
	if err == nil {
		if existingCatalogModelID != catalogModelID {
			return 0, ErrConflict
		}
		return catalogModelID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	var nameCount uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_model_names WHERE release_id=? AND catalog_model_id=?`, releaseID, catalogModelID).Scan(&nameCount); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_model_names(release_id,catalog_model_id,model_id,model_name_id,is_primary,created_at) VALUES (?,?,?,?,?,?)`, releaseID, catalogModelID, modelID, modelNameID, nameCount == 0, nowUTC())
	if err != nil {
		return 0, err
	}
	return catalogModelID, nil
}

func ensureCatalogModelOperation(ctx context.Context, tx *sql.Tx, releaseID, catalogModelID, contractID uint64, semanticDigest string, normalizationVersion uint32) (uint64, error) {
	var operationID uint64
	var existingNormalization uint32
	var existingDigest string
	err := tx.QueryRowContext(ctx, `SELECT id,normalization_version,semantic_digest FROM gw_model_operations WHERE release_id=? AND catalog_model_id=? AND operation_contract_id=?`, releaseID, catalogModelID, contractID).
		Scan(&operationID, &existingNormalization, &existingDigest)
	if err == nil {
		if existingNormalization != normalizationVersion || existingDigest != semanticDigest {
			return 0, ErrConflict
		}
		return operationID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_model_operations(release_id,catalog_model_id,operation_contract_id,normalization_version,semantic_digest,created_at) VALUES (?,?,?,?,?,?)`, releaseID, catalogModelID, contractID, normalizationVersion, semanticDigest, nowUTC())
	if err != nil {
		return 0, err
	}
	return lastID(result)
}

func ensureOperationContract(ctx context.Context, tx *sql.Tx, in CatalogSKUInput) (uint64, error) {
	var contractID uint64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM gw_operation_contracts WHERE operation_code=? AND contract_version=?`, in.OperationCode, in.ContractVersion).Scan(&contractID, &status)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_operation_contracts(operation_code,contract_version,status,created_at) VALUES (?,?,'active',?)`, in.OperationCode, in.ContractVersion, nowUTC())
		if insertErr != nil {
			return 0, insertErr
		}
		contractID, insertErr = lastID(result)
		if insertErr != nil {
			return 0, insertErr
		}
	} else if err != nil {
		return 0, err
	} else if status != "active" {
		return 0, ErrConflict
	}
	var routeContract uint64
	err = tx.QueryRowContext(ctx, `SELECT operation_contract_id FROM gw_operation_routes WHERE http_method=? AND route_template=?`, in.HTTPMethod, in.RouteTemplate).Scan(&routeContract)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `INSERT INTO gw_operation_routes(operation_contract_id,http_method,route_template,created_at) VALUES (?,?,?,?)`, contractID, in.HTTPMethod, in.RouteTemplate, nowUTC())
		return contractID, err
	}
	if err != nil {
		return 0, err
	}
	if routeContract != contractID {
		return 0, ErrConflict
	}
	return contractID, nil
}
