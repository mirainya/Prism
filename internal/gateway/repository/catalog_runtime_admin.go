package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// OfferingRuntimeStates are the three states routing distinguishes. Only
// 'active' is served (routing/unified.go joins on it), 'draining' stops new
// selection while in-flight attempts finish against their own published
// snapshot, and 'disabled' is off.
var OfferingRuntimeStates = []string{"active", "draining", "disabled"}

// OfferingRuntimeStateInput is an A-class change: it takes effect in place, with
// no release, no publication and no activation. That is deliberate and is why v2
// exempted gw_offering_runtime_state from release immutability in the first
// place — taking a broken upstream out of rotation must not require forking and
// republishing a catalog.
type OfferingRuntimeStateInput struct {
	State           string `json:"state"`
	ReasonCode      string `json:"reason_code"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (in OfferingRuntimeStateInput) Validate() error {
	if in.ExpectedVersion == 0 || !catalogIdentityPattern.MatchString(in.ReasonCode) || len(in.ReasonCode) > 64 {
		return ErrInvalidInput
	}
	for _, state := range OfferingRuntimeStates {
		if in.State == state {
			return nil
		}
	}
	return ErrInvalidInput
}

// SetOfferingRuntimeState moves one offering between runtime states and appends
// the transition to gw_offering_state_events in the same transaction.
//
// Unlike credentials and pools, whose states are deliberately one-way because a
// rotated secret is never un-rotated, every distinct pair is allowed here: an
// offering disabled by mistake has to be recoverable, and cancelling a drain is
// an ordinary operator action. A no-op transition is rejected instead, because
// there is no state change to record and the event table's
// uq_gw_offering_state_events_version would be spent on a row that says nothing.
func (s *Store) SetOfferingRuntimeState(ctx context.Context, tx *sql.Tx, offeringID uint64, in OfferingRuntimeStateInput, actorID uint64) error {
	if tx == nil || offeringID == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return err
	}
	// The release is resolved by primary key first so the row below can be locked
	// through uq_gw_offering_runtime_state_release_offering. Locking on
	// offering_id alone would match no index — release_id leads that key — and
	// InnoDB would lock every row it scanned, which is the last thing an
	// emergency disable should do to the rest of the catalog.
	var releaseID uint64
	if err := tx.QueryRowContext(ctx, `SELECT release_id FROM gw_offerings WHERE id=?`, offeringID).Scan(&releaseID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var version uint64
	var current string
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT state,state_version FROM gw_offering_runtime_state WHERE release_id=? AND offering_id=?`),
		releaseID, offeringID).Scan(&current, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if version != in.ExpectedVersion || current == in.State {
		return ErrConflict
	}
	next := version + 1
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_offering_runtime_state SET state=?,state_version=?,reason_code=?,updated_at=? WHERE release_id=? AND offering_id=? AND state_version=?`,
		in.State, next, in.ReasonCode, now, releaseID, offeringID, version)
	if err != nil {
		return fmt.Errorf("update offering runtime state: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_offering_state_events(release_id,offering_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?,?)`,
		releaseID, offeringID, next, current, in.State, in.ReasonCode, now); err != nil {
		return fmt.Errorf("record offering state event: %w", err)
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.offering.runtime_state", "offering", offeringID, ginSafeMetadata{
		"release_id": releaseID, "old_state": current, "new_state": in.State, "reason_code": in.ReasonCode,
	})
}
