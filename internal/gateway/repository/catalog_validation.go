package repository

import (
	"context"
	"fmt"
)

// CheckCatalogValidations requires every Offering to have current commercial
// evidence and at least one executable credential with current entitlement
// evidence. Expiry is evaluated by the database clock used for routing.
func CheckCatalogValidations(ctx context.Context, db CatalogPricingQuery, releaseID uint64) error {
	if db == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	var offerings, invalid uint64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN
NOT EXISTS (
  SELECT 1 FROM gw_commercial_state cs
  JOIN gw_commercial_validation_events ce ON ce.id=cs.latest_event_id AND ce.commercial_fingerprint=cs.commercial_fingerprint
  WHERE cs.commercial_fingerprint=o.commercial_fingerprint AND cs.state='valid' AND ce.state='valid' AND ce.valid_until>UTC_TIMESTAMP(3)
) OR NOT EXISTS (
  SELECT 1 FROM gw_credentials c
  JOIN gw_credential_secret_identities si ON si.id=c.secret_identity_id AND si.status='active'
  JOIN gw_credential_versions cv ON cv.id=c.current_version_id AND cv.credential_id=c.id AND cv.status='active' AND (cv.valid_until IS NULL OR cv.valid_until>UTC_TIMESTAMP(3))
  JOIN gw_credential_purpose_grants pg ON pg.credential_id=c.id AND pg.purpose='execution' AND pg.status='active'
  JOIN gw_credential_entitlement_state es ON es.credential_id=c.id AND es.credential_version_id=cv.id AND es.entitlement_fingerprint=o.entitlement_fingerprint AND es.state='valid'
  JOIN gw_credential_validation_events ee ON ee.id=es.latest_event_id AND ee.credential_id=es.credential_id AND ee.credential_version_id=es.credential_version_id AND ee.entitlement_fingerprint=es.entitlement_fingerprint
  WHERE c.credential_pool_id=o.credential_pool_id AND c.status='active' AND ee.state='valid' AND ee.valid_until>UTC_TIMESTAMP(3)
) THEN 1 ELSE 0 END),0)
FROM gw_offerings o WHERE o.release_id=?`, releaseID).Scan(&offerings, &invalid)
	if err != nil {
		return err
	}
	if offerings == 0 || invalid != 0 {
		return fmt.Errorf("%w: catalog entitlement or commercial validation is incomplete", ErrConflict)
	}
	return nil
}
