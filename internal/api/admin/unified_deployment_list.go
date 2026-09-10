package admin

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

func ListUnifiedDeployments(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_generations`).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT g.id,g.generation_no,g.status,g.semantic_version,g.semantic_digest,g.member_frozen_at,g.created_at,
(SELECT COUNT(*) FROM gw_deployment_members m WHERE m.deployment_generation_id=g.id),
COALESCE((SELECT m.id FROM gw_deployment_members m WHERE m.deployment_generation_id=g.id AND m.instance_id=? AND m.role=? LIMIT 1),0)
FROM gw_deployment_generations g ORDER BY g.generation_no DESC LIMIT ? OFFSET ?`, identity.InstanceID, identity.Role, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, generation, members, currentMemberID uint64
		var status, version, digest string
		var frozen, created sql.NullTime
		if err := rows.Scan(&id, &generation, &status, &version, &digest, &frozen, &created, &members, &currentMemberID); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{"id": id, "generation_no": generation, "status": status, "semantic_version": version, "semantic_digest": digest, "member_frozen_at": nullableTime(frozen), "created_at": nullableTime(created), "member_count": members, "current_member_id": currentMemberID})
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}
