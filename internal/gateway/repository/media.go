package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type MediaAssetInput struct {
	UserID, TokenID                                        uint64
	Purpose, ObjectKey, ObjectVersion, ContentType, SHA256 string
	ContentLength                                          uint64
	RetentionUntil                                         *time.Time
}

func (s *Store) CreateMediaAsset(ctx context.Context, tx *sql.Tx, in MediaAssetInput) (uint64, error) {
	if tx == nil || in.UserID == 0 || in.TokenID == 0 || in.ObjectKey == "" || len(in.ObjectKey) > 512 || len(in.ObjectVersion) > 255 || in.ContentType == "" || len(in.ContentType) > 128 || in.ContentLength == 0 || !validHexDigest(in.SHA256, 32) {
		return 0, ErrInvalidInput
	}
	if in.Purpose != "input" && in.Purpose != "result" && in.Purpose != "file" {
		return 0, ErrInvalidInput
	}
	if in.RetentionUntil != nil && !in.RetentionUntil.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	var retention any
	if in.RetentionUntil != nil {
		retention = in.RetentionUntil.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_media_assets(user_id,token_id,purpose,object_key,object_version,content_type,content_length,sha256,state,retention_until,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?, 'staging',?,?,?)`, in.UserID, in.TokenID, in.Purpose, in.ObjectKey, emptyAsNull(in.ObjectVersion), in.ContentType, in.ContentLength, in.SHA256, retention, nowUTC(), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create media asset: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,NULL,'staging',1,'media_asset_allocated',?)`, id, nowUTC())); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) TransitionMediaAsset(ctx context.Context, tx *sql.Tx, assetID uint64, from, to string) error {
	return s.TransitionMediaAssetWithReason(ctx, tx, assetID, from, to, "media_asset_transition")
}

func (s *Store) TransitionMediaAssetWithReason(ctx context.Context, tx *sql.Tx, assetID uint64, from, to, reason string) error {
	if tx == nil || assetID == 0 || !validMediaState(from) || !validMediaState(to) {
		return ErrInvalidInput
	}
	reason = strings.ToLower(strings.TrimSpace(reason))
	if !catalogIdentityPattern.MatchString(reason) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "staging" && (to == "active" || to == "deleting") || from == "active" && to == "deleting" || from == "deleting" && to == "deleted") {
		return ErrConflict
	}
	var state string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_media_assets WHERE id=? FOR UPDATE`, assetID).Scan(&state, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != from || version == ^uint64(0) {
		return ErrConflict
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_media_assets SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state=? AND state_version=?`, to, now, assetID, from, version)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, assetID, from, to, version+1, reason, now))
}
func validMediaState(v string) bool {
	return v == "staging" || v == "active" || v == "deleting" || v == "deleted"
}

type ManagedCopyAssetRecord struct {
	ID, UserID, TokenID, StateVersion                     uint64
	ObjectKey, StorageLocator, ObjectVersion, ContentType string
	SHA256, State                                         string
	ContentLength                                         uint64
}

// ReserveManagedCopyAsset creates the durable upload intent before any object
// storage write. The logical object key is deterministic for one Attempt and
// result ordinal, so a retry reuses the same staging fact.
func (s *Store) ReserveManagedCopyAsset(ctx context.Context, tx *sql.Tx, attemptID uint64, in MediaAssetInput) (ManagedCopyAssetRecord, error) {
	if tx == nil || attemptID == 0 || in.Purpose != "result" {
		return ManagedCopyAssetRecord{}, ErrInvalidInput
	}
	var callID, userID, tokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.token_id FROM gw_api_call_attempts a JOIN gw_api_calls c ON c.id=a.call_id WHERE a.id=? FOR SHARE`, attemptID).Scan(&callID, &userID, &tokenID); err != nil {
		return ManagedCopyAssetRecord{}, err
	}
	if callID == 0 || in.UserID != userID || in.TokenID != tokenID {
		return ManagedCopyAssetRecord{}, ErrConflict
	}
	var out ManagedCopyAssetRecord
	var locator, version sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,user_id,token_id,object_key,storage_locator,object_version,content_type,content_length,sha256,state,state_version FROM gw_media_assets WHERE object_key=? FOR UPDATE`, in.ObjectKey).
		Scan(&out.ID, &out.UserID, &out.TokenID, &out.ObjectKey, &locator, &version, &out.ContentType, &out.ContentLength, &out.SHA256, &out.State, &out.StateVersion)
	if err == sql.ErrNoRows {
		id, createErr := s.CreateMediaAsset(ctx, tx, in)
		if createErr != nil {
			return ManagedCopyAssetRecord{}, createErr
		}
		return ManagedCopyAssetRecord{ID: id, UserID: userID, TokenID: tokenID, ObjectKey: in.ObjectKey, ContentType: in.ContentType, ContentLength: in.ContentLength, SHA256: in.SHA256, State: "staging", StateVersion: 1}, nil
	}
	if err != nil {
		return ManagedCopyAssetRecord{}, err
	}
	if locator.Valid {
		out.StorageLocator = locator.String
	}
	if version.Valid {
		out.ObjectVersion = version.String
	}
	if out.UserID != userID || out.TokenID != tokenID || out.ContentType != in.ContentType || out.ContentLength != in.ContentLength || !strings.EqualFold(out.SHA256, in.SHA256) || out.State != "staging" && out.State != "active" {
		return ManagedCopyAssetRecord{}, ErrConflict
	}
	return out, nil
}

func (s *Store) RecordManagedCopyUpload(ctx context.Context, tx *sql.Tx, assetID uint64, locator, objectVersion string) error {
	locator = strings.TrimSpace(locator)
	objectVersion = strings.TrimSpace(objectVersion)
	if tx == nil || assetID == 0 || locator == "" || len(locator) > 2048 || len(objectVersion) > 255 {
		return ErrInvalidInput
	}
	var state string
	var currentLocator sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT state,storage_locator FROM gw_media_assets WHERE id=? FOR UPDATE`, assetID).Scan(&state, &currentLocator); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "staging" {
		return ErrConflict
	}
	if currentLocator.Valid && currentLocator.String != "" {
		if currentLocator.String == locator {
			return nil
		}
		return ErrConflict
	}
	return requireOneRow(tx.ExecContext(ctx, `UPDATE gw_media_assets SET storage_locator=?,object_version=?,updated_at=? WHERE id=? AND state='staging' AND storage_locator IS NULL`, locator, emptyAsNull(objectVersion), nowUTC(), assetID))
}

type MediaAssetRefInput struct {
	MediaAssetID, UserID, TokenID uint64
	Role                          string
	Ordinal                       uint32
	CallID, ResultDeliveryID      *uint64
}

func (s *Store) AddMediaAssetRef(ctx context.Context, tx *sql.Tx, in MediaAssetRefInput) (uint64, error) {
	if tx == nil || in.MediaAssetID == 0 || in.UserID == 0 || in.TokenID == 0 || (in.Role != "input" && in.Role != "result" && in.Role != "file") {
		return 0, ErrInvalidInput
	}
	if (in.CallID == nil) == (in.ResultDeliveryID == nil) {
		return 0, ErrInvalidInput
	}
	if in.CallID != nil {
		var parentUserID, parentTokenID uint64
		if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR SHARE`, *in.CallID).Scan(&parentUserID, &parentTokenID); err == sql.ErrNoRows {
			return 0, ErrNotFound
		} else if err != nil {
			return 0, err
		}
		if parentUserID != in.UserID || parentTokenID != in.TokenID {
			return 0, ErrConflict
		}
	}
	var assetUserID, assetTokenID uint64
	var purpose, state string
	var retention sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id,purpose,state,retention_until FROM gw_media_assets WHERE id=? FOR SHARE`, in.MediaAssetID).Scan(&assetUserID, &assetTokenID, &purpose, &state, &retention); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if assetUserID != in.UserID || assetTokenID != in.TokenID || retention.Valid && !retention.Time.After(nowUTC()) {
		return 0, ErrConflict
	}
	if in.CallID != nil {
		if in.Role != "input" || (purpose != "input" && purpose != "file") || state != "active" {
			return 0, ErrConflict
		}
	} else {
		if in.Role != "result" || purpose != "result" || state != "staging" && state != "active" {
			return 0, ErrConflict
		}
		var parentUserID, parentTokenID uint64
		if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_result_deliveries WHERE id=? FOR SHARE`, *in.ResultDeliveryID).Scan(&parentUserID, &parentTokenID); err == sql.ErrNoRows {
			return 0, ErrNotFound
		} else if err != nil {
			return 0, err
		}
		if parentUserID != in.UserID || parentTokenID != in.TokenID {
			return 0, ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_media_asset_refs(media_asset_id,user_id,token_id,role,ordinal,call_id,result_delivery_id,created_at) VALUES (?,?,?,?,?,?,?,?)`, in.MediaAssetID, in.UserID, in.TokenID, in.Role, in.Ordinal, nullableID(in.CallID), nullableID(in.ResultDeliveryID), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("add media asset ref: %w", err)
	}
	return lastID(result)
}
