package admin

import (
	"database/sql"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// SetUnifiedOfferingRuntimeState is the A-class emergency switch: it moves one
// offering between active, draining and disabled in place, with no release, no
// publication and no activation. gw_offering_runtime_state is the one table v2
// exempted from release immutability precisely so taking a broken upstream out of
// rotation does not require forking and republishing a catalog.
//
// The release must be published — a draft's offerings are not routable, so
// changing their runtime state records an event that means nothing, and it would
// also bypass lockCatalogDraft's rule that draft content only changes through the
// draft's own config_version.
func SetUnifiedOfferingRuntimeState(c *gin.Context) {
	offeringID, err := resp.ParseUintParam(c, "offering_id")
	if err != nil {
		return
	}
	var in repository.OfferingRuntimeStateInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.State = strings.ToLower(strings.TrimSpace(in.State))
	in.ReasonCode = strings.TrimSpace(in.ReasonCode)
	// Validated here as well as in the store, so a malformed emergency disable is
	// refused without opening a transaction and taking locks on the way to the
	// same answer.
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		ctx := c.Request.Context()
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT r.status FROM gw_offerings o JOIN gw_catalog_releases r ON r.id=o.release_id WHERE o.id=? FOR SHARE`, offeringID).Scan(&status); err == sql.ErrNoRows {
			return 0, repository.ErrNotFound
		} else if err != nil {
			return 0, err
		}
		if status != "published" {
			return 0, repository.ErrConflict
		}
		return uint64(offeringID), store.SetOfferingRuntimeState(ctx, tx, uint64(offeringID), in, actor)
	})
}
