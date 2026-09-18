package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// ModelMetaUpdate is an A-class change to the presentation plane. gw_model_meta
// never takes part in routing (internal/gateway/routing does not read it), so an
// edit here cannot move a call to a different upstream or change what it costs —
// which is exactly why it can be applied in place instead of through a release.
//
// The identity is model_name, the table's primary key. There is no surrogate id
// to address, and inventing one would create a second name for the same row.
type ModelMetaUpdate struct {
	DisplayName string `json:"display_name"`
	GroupName   string `json:"group_name"`
	Sort        int    `json:"sort"`
	// Status is the display flag, not a routing switch: hiding a model from the
	// console list does not stop calls to it. Taking an upstream out of rotation
	// is SetOfferingRuntimeState.
	Status          int8   `json:"status"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (in ModelMetaUpdate) Validate() error {
	if in.ExpectedVersion == 0 || !validDisplayName(in.DisplayName) || in.Status != 0 && in.Status != 1 {
		return ErrInvalidInput
	}
	// An empty group means "group by source channel", which is the stored default,
	// so it is accepted where a display name is not.
	if in.GroupName != "" && !validDisplayName(in.GroupName) || utf8.RuneCountInString(in.GroupName) > 80 {
		return ErrInvalidInput
	}
	// varchar(100), and sort is a display order rather than an arbitrary integer.
	if utf8.RuneCountInString(in.DisplayName) > 100 || in.Sort < 0 || in.Sort > 1000000 {
		return ErrInvalidInput
	}
	return nil
}

// UpdateModelMeta rewrites one presentation row under an optimistic lock.
//
// The version column exists because updated_at cannot do this job: it is a
// millisecond timestamp, so two edits landing in the same millisecond would each
// see the other's token as unchanged and silently overwrite one another.
func (s *Store) UpdateModelMeta(ctx context.Context, tx *sql.Tx, modelName string, in ModelMetaUpdate, actorID uint64) error {
	if tx == nil || actorID == 0 || !catalogIdentityPattern.MatchString(modelName) || utf8.RuneCountInString(modelName) > 80 {
		return ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return err
	}
	var version uint64
	var displayName, groupName string
	var sortOrder int
	var status int8
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT display_name,group_name,sort,status,config_version FROM gw_model_meta WHERE model_name=?`),
		modelName).Scan(&displayName, &groupName, &sortOrder, &status, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if version != in.ExpectedVersion {
		return ErrConflict
	}
	// A no-op edit is accepted rather than rejected: unlike a runtime-state
	// transition it appends nothing to an event log, so there is no wasted version
	// to protect, and returning a conflict for "saved the form unchanged" would be
	// noise. The version still advances so a concurrent editor sees the write.
	result, err := tx.ExecContext(ctx, `UPDATE gw_model_meta SET display_name=?,group_name=?,sort=?,status=?,config_version=config_version+1,updated_at=? WHERE model_name=? AND config_version=?`,
		in.DisplayName, in.GroupName, in.Sort, in.Status, nowUTC(), modelName, version)
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
	// The whole row is rewritten, so the audit entry has to carry what it replaced
	// or the change is unreviewable after the fact.
	return recordCatalogAdminModelChange(ctx, tx, actorID, modelName, ginSafeMetadata{
		"before": ginSafeMetadata{"display_name": displayName, "group_name": groupName, "sort": sortOrder, "status": status},
		"after":  ginSafeMetadata{"display_name": in.DisplayName, "group_name": in.GroupName, "sort": in.Sort, "status": in.Status},
	})
}

// recordCatalogAdminModelChange exists because recordCatalogAdminChange keys the
// audit row by a numeric id, and this table's identity is its name.
func recordCatalogAdminModelChange(ctx context.Context, tx *sql.Tx, actorID uint64, modelName string, metadata ginSafeMetadata) error {
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_type,actor_user_id,action,resource_type,resource_id,outcome,http_status,metadata,created_at) VALUES ('user',?,'unified.model_meta.update','model_meta',?,'success',200,?,?)`,
		actorID, modelName, string(body), nowUTC()); err != nil {
		return fmt.Errorf("record gateway configuration audit: %w", err)
	}
	return nil
}
