package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// recordMappingProof binds an existing immutable object mapping to the import
// run that verified its exact source revision. The insert is replay-safe for a
// failed run that is retried with the same source snapshot.
func recordMappingProof(ctx context.Context, tx *sql.Tx, runID, mapID int64, sourceHMAC string, observedAt time.Time) error {
	if tx == nil || runID <= 0 || mapID <= 0 || len(sourceHMAC) != 64 || observedAt.IsZero() {
		return fmt.Errorf("invalid migration mapping proof")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_mapping_proofs(run_id,object_map_id,source_hmac,observed_at)
SELECT ?,?,?,? FROM gw_migration_source_revisions
WHERE object_map_id=? AND source_hmac=?
  AND NOT EXISTS (SELECT 1 FROM gw_migration_mapping_proofs WHERE run_id=? AND object_map_id=?)
LIMIT 1`, runID, mapID, sourceHMAC, observedAt.UTC(), mapID, sourceHMAC, runID, mapID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 1 {
		return nil
	}
	if rows != 0 {
		return fmt.Errorf("migration mapping proof inserted %d rows", rows)
	}
	var existing string
	if err := tx.QueryRowContext(ctx, `SELECT source_hmac FROM gw_migration_mapping_proofs WHERE run_id=? AND object_map_id=?`, runID, mapID).Scan(&existing); err != nil {
		return fmt.Errorf("migration mapping proof source revision is missing: %w", err)
	}
	if existing != sourceHMAC {
		return fmt.Errorf("migration mapping proof source revision conflict")
	}
	return nil
}
