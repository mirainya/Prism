package repository

import (
	"context"
	"database/sql"
)

// ReadinessQuery is shared by read-only checks and activation transactions.
type ReadinessQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// LegacyCredentialCryptoRequired reports whether any credential still relies
// on an encrypted blob because it has no direct secret. Once every credential
// has a direct secret, the retired credential keyring is no longer a runtime
// dependency even if old encrypted versions remain for audit history.
func LegacyCredentialCryptoRequired(ctx context.Context, db ReadinessQuery) (bool, error) {
	if db == nil {
		return false, ErrConflict
	}
	var required bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM gw_credentials c
JOIN gw_credential_versions v ON v.credential_id=c.id AND v.encrypted_blob_id IS NOT NULL
WHERE c.status IN ('active','draining') AND (c.secret IS NULL OR c.secret='')
)`).Scan(&required)
	return required, err
}

// CheckRuntimeKeyReadiness requires a current payload keyring. The legacy
// credential keyring remains required only while an active credential still
// depends on encrypted storage.
func CheckRuntimeKeyReadiness(ctx context.Context, db ReadinessQuery) error {
	_, err := checkRuntimeKeyrings(ctx, db)
	return err
}

func checkRuntimeKeyrings(ctx context.Context, db ReadinessQuery) (bool, error) {
	if db == nil {
		return false, ErrConflict
	}
	requireCredential, err := LegacyCredentialCryptoRequired(ctx, db)
	if err != nil {
		return false, err
	}
	var rings uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current'
WHERE k.purpose='gateway-payload' OR (? AND k.purpose='gateway-credential')`, requireCredential).Scan(&rings); err != nil {
		return false, err
	}
	wantRings := uint64(1)
	if requireCredential {
		wantRings++
	}
	if rings != wantRings {
		return false, ErrConflict
	}
	return requireCredential, nil
}

// CheckCryptoReadiness is retained for legacy deployment administration. New
// data-plane readiness uses CheckRuntimeKeyReadiness and does not consume
// generation member proofs.
func CheckCryptoReadiness(ctx context.Context, db ReadinessQuery, generationID uint64) error {
	if generationID == 0 {
		return ErrConflict
	}
	requireCredential, err := checkRuntimeKeyrings(ctx, db)
	if err != nil {
		return err
	}
	var members, ready uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN NOT EXISTS (
SELECT 1 FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND (v.key_version=k.current_version OR v.status='readable')
WHERE (k.purpose='gateway-payload' OR (? AND k.purpose='gateway-credential')) AND (
SELECT COUNT(DISTINCT r.operation) FROM crypto_key_readiness r
WHERE r.deployment_generation_id=m.deployment_generation_id AND r.deployment_member_id=m.id
AND r.keyring_id=k.id AND r.key_version=v.key_version AND r.status='ready' AND r.expires_at>CURRENT_TIMESTAMP(3)
AND (v.key_version=k.current_version AND r.operation IN ('mac','wrap','unwrap','encrypt','decrypt')
OR v.key_version<>k.current_version AND r.operation IN ('mac','unwrap','decrypt'))
) <> CASE WHEN v.key_version=k.current_version THEN 5 ELSE 3 END
) THEN 1 ELSE 0 END),0)
FROM gw_deployment_members m WHERE m.deployment_generation_id=?`, requireCredential, generationID).Scan(&members, &ready); err != nil {
		return err
	}
	if members == 0 || members != ready {
		return ErrConflict
	}
	return nil
}
