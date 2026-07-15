package console

import (
	stdErrors "errors"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

var observabilityService = service.NewObservabilityService()

func ListAPIAccessLogs(c *gin.Context) {
	var req service.ListAPIAccessLogsRequest
	if !bindObservabilityQuery(c, &req.ObservabilityListFilter, &req) {
		return
	}
	result, err := observabilityService.ListAPIAccessLogs(&req, observabilityScope(c))
	writeObservabilityResult(c, result, err)
}

func ListAuditEvents(c *gin.Context) {
	var req service.ListAuditEventsRequest
	if !bindObservabilityQuery(c, &req.ObservabilityListFilter, &req) {
		return
	}
	result, err := observabilityService.ListAuditEvents(&req, observabilityScope(c))
	writeObservabilityResult(c, result, err)
}

func ListBalanceEntries(c *gin.Context) {
	var req service.ListBalanceEntriesRequest
	if !bindObservabilityQuery(c, &req.ObservabilityListFilter, &req) {
		return
	}
	result, err := observabilityService.ListBalanceEntries(&req, observabilityScope(c))
	writeObservabilityResult(c, result, err)
}

func bindObservabilityQuery(c *gin.Context, filter *service.ObservabilityListFilter, target any) bool {
	actorUserID := middleware.GetUserID(c)
	if actorUserID == 0 {
		resp.Unauthorized(c, pkgErrors.ErrUnauthorized)
		return false
	}
	if err := c.ShouldBindQuery(target); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid query parameters"))
		return false
	}
	if middleware.GetUserRole(c) != string(model.UserRoleAdmin) &&
		filter.UserID > 0 && filter.UserID != actorUserID {
		resp.Forbidden(c, pkgErrors.WithMessage(pkgErrors.ErrNoPermission, "no permission to access this user's records"))
		return false
	}
	return true
}

func observabilityScope(c *gin.Context) service.ObservabilityScope {
	return service.ObservabilityScope{
		ActorUserID: middleware.GetUserID(c),
		IsAdmin:     middleware.GetUserRole(c) == string(model.UserRoleAdmin),
	}
}

func writeObservabilityResult(c *gin.Context, result any, err error) {
	if err == nil {
		resp.Success(c, result)
		return
	}
	switch {
	case stdErrors.Is(err, service.ErrObservabilityInvalidInput):
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid observability query"))
	case stdErrors.Is(err, service.ErrObservabilityAccessDenied):
		resp.Forbidden(c, pkgErrors.WithMessage(pkgErrors.ErrNoPermission, "no permission to access these records"))
	default:
		resp.InternalError(c, pkgErrors.ErrInternalError)
	}
}
