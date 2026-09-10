package repository

import (
	"context"
	"database/sql"
)

type CallInputAsset struct {
	ID            uint64
	Ordinal       uint32
	ObjectLocator string
	ContentType   string
	ContentLength uint64
	SHA256        string
}

// ResolveFileAsset returns the immutable object referenced by an owned,
// processed Files API resource. The caller later creates a Call reference in
// the same transaction as submission, which protects the object independently
// of the file resource lifecycle.
func (s *Store) ResolveFileAsset(ctx context.Context, userID, tokenID uint64, fileID string) (uint64, error) {
	if s == nil || userID == 0 || tokenID == 0 || fileID == "" {
		return 0, ErrInvalidInput
	}
	var assetID uint64
	err := s.db.QueryRowContext(ctx, `SELECT a.id
FROM gw_file_resources f
JOIN gw_media_asset_refs ref ON ref.ai_file_id=f.id AND ref.user_id=f.user_id AND ref.token_id=f.token_id AND ref.role='file' AND ref.ordinal=0
JOIN gw_media_assets a ON a.id=ref.media_asset_id AND a.user_id=ref.user_id AND a.token_id=ref.token_id
WHERE f.id=? AND f.user_id=? AND f.token_id=? AND f.status='processed' AND a.state='active'
  AND (a.retention_until IS NULL OR a.retention_until>UTC_TIMESTAMP(3))`, fileID, userID, tokenID).Scan(&assetID)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	return assetID, err
}

// ReadCallInputAssets loads only bounded object metadata. Object bytes are
// fetched and verified by the worker immediately before transport preparation.
func (s *Store) ReadCallInputAssets(ctx context.Context, callID, userID, tokenID uint64) ([]CallInputAsset, error) {
	if s == nil || callID == 0 || userID == 0 || tokenID == 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,ref.ordinal,
COALESCE(NULLIF(a.storage_locator,''),a.object_key),a.content_type,a.content_length,a.sha256
FROM gw_media_asset_refs ref
JOIN gw_media_assets a ON a.id=ref.media_asset_id AND a.user_id=ref.user_id AND a.token_id=ref.token_id
WHERE ref.call_id=? AND ref.user_id=? AND ref.token_id=? AND ref.role='input'
  AND a.state='active'
ORDER BY ref.ordinal`, callID, userID, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallInputAsset
	for rows.Next() {
		var item CallInputAsset
		if err := rows.Scan(&item.ID, &item.Ordinal, &item.ObjectLocator, &item.ContentType, &item.ContentLength, &item.SHA256); err != nil {
			return nil, err
		}
		if item.ID == 0 || item.Ordinal != uint32(len(out)) || item.ObjectLocator == "" || item.ContentType == "" || item.ContentLength == 0 || !validHexDigest(item.SHA256, 32) {
			return nil, ErrConflict
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
