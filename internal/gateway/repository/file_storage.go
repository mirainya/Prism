package repository

import (
	"context"
	"database/sql"
	"strings"
)

type TokenFileStorage struct {
	UserID, TokenID uint64
	APIKey          string
}

func (s *Store) ReadTokenFileStorage(ctx context.Context, tx *sql.Tx, userID, tokenID uint64) (TokenFileStorage, error) {
	if s == nil || tx == nil || userID == 0 || tokenID == 0 {
		return TokenFileStorage{}, ErrInvalidInput
	}
	storage := TokenFileStorage{UserID: userID, TokenID: tokenID}
	if err := tx.QueryRowContext(ctx, s.forShare(`SELECT COALESCE(xfs_api_key,'') FROM tokens WHERE id=? AND user_id=? AND deleted_at IS NULL`), tokenID, userID).Scan(&storage.APIKey); err == sql.ErrNoRows {
		return TokenFileStorage{}, ErrNotFound
	} else if err != nil {
		return TokenFileStorage{}, err
	}
	storage.APIKey = strings.TrimSpace(storage.APIKey)
	return storage, nil
}

func (s *Store) ReadAttemptFileStorage(ctx context.Context, attemptID uint64) (TokenFileStorage, error) {
	if s == nil || s.db == nil || attemptID == 0 {
		return TokenFileStorage{}, ErrInvalidInput
	}
	var storage TokenFileStorage
	err := s.db.QueryRowContext(ctx, `SELECT c.user_id,c.token_id,COALESCE(c.xfs_api_key,'')
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id
WHERE a.id=?`, attemptID).Scan(&storage.UserID, &storage.TokenID, &storage.APIKey)
	if err == sql.ErrNoRows {
		return TokenFileStorage{}, ErrNotFound
	}
	if err != nil {
		return TokenFileStorage{}, err
	}
	storage.APIKey = strings.TrimSpace(storage.APIKey)
	if storage.UserID == 0 || storage.TokenID == 0 {
		return TokenFileStorage{}, ErrConflict
	}
	return storage, nil
}
