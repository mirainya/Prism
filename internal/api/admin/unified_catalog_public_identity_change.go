package admin

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// ChangeUnifiedPublicModelIdentities directly renames several public model
// identities in one active-catalog transaction.
func ChangeUnifiedPublicModelIdentities(c *gin.Context) {
	var in repository.PublicModelIdentityChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangePublicModelIdentities(c.Request.Context(), tx, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}
