package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type IdempotencyKeyAlias struct {
	KeyHMAC, RequestHMAC string
	HMACKeyVersion       uint32
}

type IdempotencyInput struct {
	TokenID, OperationContractID   uint64
	KeyHMAC, RequestHMAC           string
	HMACKeyVersion                 uint32
	KeyAliases                     []IdempotencyKeyAlias
	ReplayExpiresAt, KeyReuseAfter *time.Time
}

type IdempotencyReservation struct {
	ID     uint64
	CallID *uint64
	Reused bool
}

type idempotencyFact struct {
	ID                             uint64
	CallID                         *uint64
	RequestHMAC, Status            string
	HMACKeyVersion                 uint32
	ReplayExpiresAt, KeyReuseAfter *time.Time
}

// ResolveOperationContract resolves the stable HTTP operation identity without
// consulting the active catalog. Idempotent replays therefore remain available
// when a later release removes or renames a model.
func (s *Store) ResolveOperationContract(ctx context.Context, method, route string) (uint64, error) {
	if s == nil || method == "" || route == "" {
		return 0, ErrInvalidInput
	}
	var id uint64
	if err := s.db.QueryRowContext(ctx, `SELECT operation_contract_id FROM gw_operation_routes WHERE http_method=? AND route_template=?`, method, route).Scan(&id); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, ErrConflict
	}
	return id, nil
}

// FindIdempotency resolves an existing fact through its versioned aliases. It
// never creates a fact or Call, but it may add a missing alias for a retained
// key version while the original raw key is present in the request.
func (s *Store) FindIdempotency(ctx context.Context, in IdempotencyInput) (IdempotencyReservation, error) {
	aliases, err := normalizeIdempotencyAliases(in)
	if err != nil || s == nil || in.TokenID == 0 || in.OperationContractID == 0 {
		return IdempotencyReservation{}, ErrInvalidInput
	}
	var out IdempotencyReservation
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		fact, err := s.findIdempotencyFact(ctx, tx, in.TokenID, in.OperationContractID, aliases)
		if err != nil {
			return err
		}
		if err := validateIdempotencyFact(fact, aliases, nowUTC()); err != nil {
			return err
		}
		if err := s.ensureIdempotencyAliases(ctx, tx, fact.ID, in.TokenID, in.OperationContractID, aliases); err != nil {
			return err
		}
		out = IdempotencyReservation{ID: fact.ID, CallID: fact.CallID, Reused: true}
		return nil
	})
	return out, err
}

// ReserveIdempotency creates the one durable idempotency fact. A key can only
// be reused for byte-identical normalized input; a different request is a
// conflict and never reaches routing or billing.
func (s *Store) ReserveIdempotency(ctx context.Context, tx *sql.Tx, in IdempotencyInput) (IdempotencyReservation, error) {
	aliases, err := normalizeIdempotencyAliases(in)
	if s == nil || tx == nil || err != nil || in.TokenID == 0 || in.OperationContractID == 0 {
		return IdempotencyReservation{}, ErrInvalidInput
	}
	fact, err := s.findIdempotencyFact(ctx, tx, in.TokenID, in.OperationContractID, aliases)
	if err == nil {
		if err := validateIdempotencyFact(fact, aliases, nowUTC()); err != nil {
			return IdempotencyReservation{}, err
		}
		if err := s.ensureIdempotencyAliases(ctx, tx, fact.ID, in.TokenID, in.OperationContractID, aliases); err != nil {
			return IdempotencyReservation{}, err
		}
		return IdempotencyReservation{ID: fact.ID, CallID: fact.CallID, Reused: true}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return IdempotencyReservation{}, err
	}
	now := nowUTC()
	var replay, reuse any
	if in.ReplayExpiresAt != nil {
		replay = in.ReplayExpiresAt.UTC()
	}
	if in.KeyReuseAfter != nil {
		reuse = in.KeyReuseAfter.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_call_idempotencies(token_id,operation_contract_id,key_hmac,hmac_key_version,request_hmac,status,replay_expires_at,key_reuse_after,created_at,updated_at) VALUES (?,?,?,?,?,'active',?,?,?,?)`, in.TokenID, in.OperationContractID, in.KeyHMAC, in.HMACKeyVersion, in.RequestHMAC, replay, reuse, now, now)
	if err != nil {
		winner, readErr := s.findIdempotencyFact(ctx, tx, in.TokenID, in.OperationContractID, aliases)
		if readErr == nil {
			if validateErr := validateIdempotencyFact(winner, aliases, now); validateErr != nil {
				return IdempotencyReservation{}, validateErr
			}
			if aliasErr := s.ensureIdempotencyAliases(ctx, tx, winner.ID, in.TokenID, in.OperationContractID, aliases); aliasErr != nil {
				return IdempotencyReservation{}, aliasErr
			}
			return IdempotencyReservation{ID: winner.ID, CallID: winner.CallID, Reused: true}, nil
		}
		return IdempotencyReservation{}, fmt.Errorf("reserve idempotency: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return IdempotencyReservation{}, err
	}
	if err := s.ensureIdempotencyAliases(ctx, tx, id, in.TokenID, in.OperationContractID, aliases); err != nil {
		return IdempotencyReservation{}, err
	}
	return IdempotencyReservation{ID: id}, nil
}

func normalizeIdempotencyAliases(in IdempotencyInput) ([]IdempotencyKeyAlias, error) {
	if !validHexDigest(in.KeyHMAC, 32) || !validHexDigest(in.RequestHMAC, 32) || in.HMACKeyVersion == 0 {
		return nil, ErrInvalidInput
	}
	byVersion := make(map[uint32]IdempotencyKeyAlias, len(in.KeyAliases)+1)
	byVersion[in.HMACKeyVersion] = IdempotencyKeyAlias{
		KeyHMAC: in.KeyHMAC, RequestHMAC: in.RequestHMAC, HMACKeyVersion: in.HMACKeyVersion,
	}
	for _, alias := range in.KeyAliases {
		if alias.HMACKeyVersion == 0 || !validHexDigest(alias.KeyHMAC, 32) || !validHexDigest(alias.RequestHMAC, 32) {
			return nil, ErrInvalidInput
		}
		if existing, ok := byVersion[alias.HMACKeyVersion]; ok && (existing.KeyHMAC != alias.KeyHMAC || existing.RequestHMAC != alias.RequestHMAC) {
			return nil, ErrInvalidInput
		}
		byVersion[alias.HMACKeyVersion] = alias
	}
	versions := make([]int, 0, len(byVersion))
	for version := range byVersion {
		versions = append(versions, int(version))
	}
	sort.Ints(versions)
	aliases := make([]IdempotencyKeyAlias, 0, len(versions))
	for _, version := range versions {
		aliases = append(aliases, byVersion[uint32(version)])
	}
	return aliases, nil
}

func (s *Store) findIdempotencyFact(ctx context.Context, tx *sql.Tx, tokenID, operationContractID uint64, aliases []IdempotencyKeyAlias) (idempotencyFact, error) {
	var found idempotencyFact
	for _, alias := range aliases {
		var id uint64
		var call sql.NullInt64
		var requestHMAC, status string
		var replay, reuse sql.NullTime
		err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT i.id,i.call_id,i.request_hmac,i.hmac_key_version,i.status,i.replay_expires_at,i.key_reuse_after
FROM gw_api_call_idempotency_keys k
JOIN gw_api_call_idempotencies i ON i.id=k.idempotency_id
WHERE k.token_id=? AND k.operation_contract_id=? AND k.hmac_key_version=? AND k.key_hmac=?`), tokenID, operationContractID, alias.HMACKeyVersion, alias.KeyHMAC).
			Scan(&id, &call, &requestHMAC, &found.HMACKeyVersion, &status, &replay, &reuse)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return idempotencyFact{}, err
		}
		if found.ID != 0 && found.ID != id {
			return idempotencyFact{}, ErrIdempotencyConflict
		}
		factVersion := found.HMACKeyVersion
		found = idempotencyFact{ID: id, RequestHMAC: requestHMAC, HMACKeyVersion: factVersion, Status: status}
		if call.Valid && call.Int64 > 0 {
			value := uint64(call.Int64)
			found.CallID = &value
		}
		if replay.Valid {
			value := replay.Time.UTC()
			found.ReplayExpiresAt = &value
		}
		if reuse.Valid {
			value := reuse.Time.UTC()
			found.KeyReuseAfter = &value
		}
	}
	if found.ID == 0 {
		return idempotencyFact{}, ErrNotFound
	}
	return found, nil
}

func validateIdempotencyFact(fact idempotencyFact, aliases []IdempotencyKeyAlias, now time.Time) error {
	requestHMAC := ""
	for _, alias := range aliases {
		if alias.HMACKeyVersion == fact.HMACKeyVersion {
			requestHMAC = alias.RequestHMAC
			break
		}
	}
	if fact.Status != "active" || requestHMAC == "" || fact.RequestHMAC != requestHMAC {
		return ErrIdempotencyConflict
	}
	if fact.ReplayExpiresAt != nil && !fact.ReplayExpiresAt.After(now) {
		return ErrIdempotencyExpired
	}
	return nil
}

func (s *Store) ensureIdempotencyAliases(ctx context.Context, tx *sql.Tx, factID, tokenID, operationContractID uint64, aliases []IdempotencyKeyAlias) error {
	for _, alias := range aliases {
		result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_call_idempotency_keys(idempotency_id,token_id,operation_contract_id,key_hmac,hmac_key_version,created_at) VALUES (?,?,?,?,?,?)`, factID, tokenID, operationContractID, alias.KeyHMAC, alias.HMACKeyVersion, nowUTC())
		if err == nil {
			if _, idErr := lastID(result); idErr != nil {
				return idErr
			}
			continue
		}
		var existingFact uint64
		var existingHMAC string
		lookupErr := tx.QueryRowContext(ctx, s.forUpdate(`SELECT idempotency_id,key_hmac FROM gw_api_call_idempotency_keys WHERE token_id=? AND operation_contract_id=? AND hmac_key_version=? AND key_hmac=?`), tokenID, operationContractID, alias.HMACKeyVersion, alias.KeyHMAC).Scan(&existingFact, &existingHMAC)
		if lookupErr == sql.ErrNoRows {
			lookupErr = tx.QueryRowContext(ctx, s.forUpdate(`SELECT idempotency_id,key_hmac FROM gw_api_call_idempotency_keys WHERE idempotency_id=? AND hmac_key_version=?`), factID, alias.HMACKeyVersion).Scan(&existingFact, &existingHMAC)
		}
		if lookupErr != nil {
			return err
		}
		if existingFact != factID || existingHMAC != alias.KeyHMAC {
			return ErrIdempotencyConflict
		}
	}
	return nil
}

func (s *Store) AttachIdempotencyCall(ctx context.Context, tx *sql.Tx, id, callID uint64) error {
	if tx == nil || id == 0 || callID == 0 {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_api_call_idempotencies SET call_id=?,updated_at=? WHERE id=? AND status='active' AND call_id IS NULL`, callID, nowUTC(), id)
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

// PayloadHMACKeyVersions returns every payload-HMAC key version currently
// accepted for reads: the "current" version and every "readable" version left
// active during a rotation. Callers use it to build idempotency-key aliases so
// a client's Idempotency-Key still hashes to the original fact across a
// rotation window. Ordered ascending by version.
func (s *Store) PayloadHMACKeyVersions(ctx context.Context, db DB) ([]uint32, error) {
	if s == nil || db == nil {
		return nil, ErrInvalidInput
	}
	var keyringID uint64
	if err := db.QueryRowContext(ctx, `SELECT id FROM crypto_keyring_state WHERE purpose='gateway-payload' AND current_version IS NOT NULL`).Scan(&keyringID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT key_version FROM crypto_key_versions WHERE keyring_id=? AND status IN ('current','readable') ORDER BY key_version`, keyringID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]uint32, 0, 2)
	for rows.Next() {
		var version uint32
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		if version == 0 {
			return nil, ErrConflict
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, ErrNotFound
	}
	return versions, nil
}
