package console

import (
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/mirainya/Prism/pkg/logger"
)

// GetCurrentUser 获取当前用户信息
func GetCurrentUser(c *gin.Context) {
	userID := middleware.GetUserID(c)
	user, err := userService.GetUserByID(userID)
	if err != nil {
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}

	resp.Success(c, gin.H{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
		"balance":  user.Balance,
	})
}

// ChangePassword 修改密码
func ChangePassword(c *gin.Context) {
	var req service.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	userID := middleware.GetUserID(c)
	if err := userService.ChangePassword(userID, &req); err != nil {
		if err.Error() == "incorrect old password" {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "旧密码不正确"))
			return
		}
		logger.Error("change password error: " + err.Error())
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"changed": true})
}
