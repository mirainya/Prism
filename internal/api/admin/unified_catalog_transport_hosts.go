package admin

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

func ListUnifiedCatalogTransportAllowedHosts(c *gin.Context) {
	releaseID, transportID, ok := catalogResourceIDs(c, "transport_id")
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	store, err := repository.New(db)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	result, err := store.ListCatalogTransportAllowedHosts(c.Request.Context(), releaseID, transportID)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, result)
}

func ChangeUnifiedTransportAllowedHosts(c *gin.Context) {
	var in repository.TransportAllowedHostsChange
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
		result, err = store.ChangeTransportAllowedHosts(c.Request.Context(), tx, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}
