package repository

import (
	"context"
	"database/sql"
)

// ReadinessQuery is shared by read-only checks and activation transactions.
type ReadinessQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckCryptoReadiness requires proof for each member, keyring and key version.
// Operations proved against another key/version cannot satisfy this key's needs.
func CheckCryptoReadiness(ctx context.Context, db ReadinessQuery, generationID uint64) error {
	if db == nil || generationID == 0 {
		return ErrConflict
	}
	var rings uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current'
WHERE k.purpose IN ('gateway-credential','gateway-payload')`).Scan(&rings); err != nil {
		return err
	}
	if rings != 2 {
		return ErrConflict
	}
	var members, ready uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN NOT EXISTS (
SELECT 1 FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND (v.key_version=k.current_version OR v.status='readable')
WHERE k.purpose IN ('gateway-credential','gateway-payload') AND (
SELECT COUNT(DISTINCT r.operation) FROM crypto_key_readiness r
WHERE r.deployment_generation_id=m.deployment_generation_id AND r.deployment_member_id=m.id
AND r.keyring_id=k.id AND r.key_version=v.key_version AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3)
AND (v.key_version=k.current_version AND r.operation IN ('mac','wrap','unwrap','encrypt','decrypt')
OR v.key_version<>k.current_version AND r.operation IN ('mac','unwrap','decrypt'))
) <> CASE WHEN v.key_version=k.current_version THEN 5 ELSE 3 END
) THEN 1 ELSE 0 END),0)
FROM gw_deployment_members m WHERE m.deployment_generation_id=?`, generationID).Scan(&members, &ready); err != nil {
		return err
	}
	if members == 0 || members != ready {
		return ErrConflict
	}
	return nil
}
