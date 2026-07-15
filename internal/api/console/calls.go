package console

import (
	stdErrors "errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

var apiCallService = service.NewAPICallService()

func ListAPICalls(c *gin.Context) {
	actorUserID := middleware.GetUserID(c)
	if actorUserID == 0 {
		resp.Unauthorized(c, pkgErrors.ErrUnauthorized)
		return
	}

	var req service.ListCallsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid query parameters"))
		return
	}

	isAdmin := middleware.GetUserRole(c) == string(model.UserRoleAdmin)
	if !isAdmin && req.UserID != 0 && req.UserID != actorUserID {
		resp.Forbidden(c, pkgErrors.WithMessage(pkgErrors.ErrNoPermission, "no permission to access this user's calls"))
		return
	}

	req.ActorUserID = actorUserID
	req.IsAdmin = isAdmin
	if !isAdmin {
		req.UserID = 0
	}

	result, err := apiCallService.ListCalls(&req)
	if err != nil {
		writeAPICallError(c, err)
		return
	}
	resp.Success(c, result)
}

func GetAPICall(c *gin.Context) {
	actorUserID := middleware.GetUserID(c)
	if actorUserID == 0 {
		resp.Unauthorized(c, pkgErrors.ErrUnauthorized)
		return
	}

	callID := strings.TrimSpace(c.Param("id"))
	if callID == "" {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid call id"))
		return
	}

	isAdmin := middleware.GetUserRole(c) == string(model.UserRoleAdmin)
	detail, err := apiCallService.GetCallDetail(callID, actorUserID, isAdmin)
	if err != nil {
		writeAPICallError(c, err)
		return
	}
	resp.Success(c, detail)
}

func writeAPICallError(c *gin.Context, err error) {
	switch {
	case stdErrors.Is(err, service.ErrAPICallInvalidInput):
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid call query"))
	case stdErrors.Is(err, service.ErrAPICallAccessDenied):
		resp.Forbidden(c, pkgErrors.WithMessage(pkgErrors.ErrNoPermission, "no permission to access this call"))
	case stdErrors.Is(err, service.ErrAPICallNotFound):
		resp.NotFound(c, pkgErrors.WithMessage(pkgErrors.ErrTaskNotFound, "api call not found"))
	default:
		resp.InternalError(c, pkgErrors.ErrInternalError)
	}
}
