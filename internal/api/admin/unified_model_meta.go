package admin

import (
	"database/sql"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// UpdateUnifiedModelMeta edits the presentation plane in place: display name,
// manual group, sort order and the display flag. gw_model_meta never takes part
// in routing, so nothing here can move a call to a different upstream or change
// what it costs — which is what makes it an A-class change.
//
// The route addresses the model by name rather than by a numeric id, because
// model_name is the table's primary key; there is no surrogate id to use, and
// adding one would give the same row a second identity.
func UpdateUnifiedModelMeta(c *gin.Context) {
	// Gin already unescapes the path segment, but a model name containing a slash
	// would have arrived as two segments and matched a different route, so a stray
	// escape at this point means the caller built the URL by hand.
	modelName, err := url.PathUnescape(strings.TrimSpace(c.Param("model_name")))
	if err != nil || modelName == "" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	var in repository.ModelMetaUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.GroupName = strings.TrimSpace(in.GroupName)
	// Refused before the transaction opens, so bad input does not take a row lock
	// on the way to the same answer.
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		return store.UpdateModelMeta(c.Request.Context(), tx, modelName, in, actor)
	}) {
		return
	}
	resp.Success(c, gin.H{"model_name": modelName, "config_version": in.ExpectedVersion + 1})
}
