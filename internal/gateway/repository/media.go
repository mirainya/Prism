package repository

import (
	"context"
	"database/sql"
	"fmt"
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
	return lastID(result)
}

func (s *Store) TransitionMediaAsset(ctx context.Context, tx *sql.Tx, assetID uint64, from, to string) error {
	if tx == nil || assetID == 0 || !validMediaState(from) || !validMediaState(to) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "staging" && to == "active" || from == "active" && to == "deleting" || from == "deleting" && to == "deleted") {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_media_assets SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state=?`, to, nowUTC(), assetID, from)
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
	return nil
}
func validMediaState(v string) bool {
	return v == "staging" || v == "active" || v == "deleting" || v == "deleted"
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
	var assetUserID, assetTokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_media_assets WHERE id=? FOR SHARE`, in.MediaAssetID).Scan(&assetUserID, &assetTokenID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if assetUserID != in.UserID || assetTokenID != in.TokenID {
		return 0, ErrConflict
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
	} else {
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
