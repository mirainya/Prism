package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type RuntimeRequirementInput struct {
	ShardID                                                 uint32
	Kind, Role                                              string
	ReleaseID, OperationContractID, AdapterImplementationID *uint64
	ScopeKind, ScopeKey, CompatibilityFingerprint           string
}

func (s *Store) EnsureRuntimeRequirementGuard(ctx context.Context, tx *sql.Tx, shardID uint32) error {
	if tx == nil || shardID == 0 {
		return ErrInvalidInput
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_runtime_requirement_guards(shard_id,generation,updated_at) VALUES (?,1,?) ON DUPLICATE KEY UPDATE shard_id=VALUES(shard_id)`, shardID, nowUTC())
	return err
}
func (s *Store) CreateRuntimeRequirement(ctx context.Context, tx *sql.Tx, in RuntimeRequirementInput) (uint64, error) {
	if tx == nil || in.ShardID == 0 || in.Role == "" {
		return 0, ErrInvalidInput
	}
	if in.Kind != "release" && in.Kind != "compatibility" && in.Kind != "implementation" {
		return 0, ErrInvalidInput
	}
	if in.Kind == "release" && (in.ReleaseID == nil || in.OperationContractID != nil || in.AdapterImplementationID != nil) || in.Kind == "compatibility" && (in.OperationContractID == nil || in.ScopeKind == "" || in.ScopeKey == "" || in.CompatibilityFingerprint == "" || in.ReleaseID != nil || in.AdapterImplementationID != nil) || in.Kind == "implementation" && (in.AdapterImplementationID == nil || in.ReleaseID != nil || in.OperationContractID != nil) {
		return 0, ErrInvalidInput
	}
	if err := s.EnsureRuntimeRequirementGuard(ctx, tx, in.ShardID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_runtime_requirements(shard_id,kind,role,release_id,operation_contract_id,scope_kind,scope_key,compatibility_fingerprint,adapter_implementation_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, in.ShardID, in.Kind, in.Role, nullableID(in.ReleaseID), nullableID(in.OperationContractID), emptyAsNull(in.ScopeKind), emptyAsNull(in.ScopeKey), emptyAsNull(in.CompatibilityFingerprint), nullableID(in.AdapterImplementationID), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create runtime requirement: %w", err)
	}
	return lastID(result)
}

type RuntimeRequirementRefInput struct {
	RequirementID                                                              uint64
	CallID, AttemptID, ControlPlaneRunID, ProviderStateRefID, CredentialSlotID *uint64
}

func (s *Store) AddRuntimeRequirementRef(ctx context.Context, tx *sql.Tx, in RuntimeRequirementRefInput) (uint64, error) {
	if tx == nil || in.RequirementID == 0 {
		return 0, ErrInvalidInput
	}
	n := 0
	for _, v := range []*uint64{in.CallID, in.AttemptID, in.ControlPlaneRunID, in.ProviderStateRefID, in.CredentialSlotID} {
		if v != nil {
			n++
		}
	}
	if n != 1 {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_runtime_requirement_refs(requirement_id,call_id,attempt_id,control_plane_run_id,provider_state_ref_id,credential_slot_id,state,state_version,created_at) VALUES (?,?,?,?,?,?, 'active',1,?)`, in.RequirementID, nullableID(in.CallID), nullableID(in.AttemptID), nullableID(in.ControlPlaneRunID), nullableID(in.ProviderStateRefID), nullableID(in.CredentialSlotID), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("add runtime requirement ref: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE gw_runtime_requirements SET active_ref_count=active_ref_count+1 WHERE id=?`, in.RequirementID)
	return id, err
}
func (s *Store) ReleaseRuntimeRequirementRef(ctx context.Context, tx *sql.Tx, refID uint64) error {
	if tx == nil || refID == 0 {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_runtime_requirement_refs SET state='released',state_version=state_version+1 WHERE id=? AND state='active'`, refID)
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
	_, err = tx.ExecContext(ctx, `UPDATE gw_runtime_requirements r JOIN gw_runtime_requirement_refs x ON x.requirement_id=r.id SET r.active_ref_count=GREATEST(r.active_ref_count-1,0) WHERE x.id=?`, refID)
	return err
}
