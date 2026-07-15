package console

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/mirainya/Prism/pkg/logger"
)

var userService = service.NewUserService()

func Register(c *gin.Context) {
	var req service.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	user, err := userService.Register(&req)
	if err != nil {
		if err.Error() == "username already exists" {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
			return
		}
		logger.Error("register error: " + err.Error())
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	c.Set(middleware.ContextKeyUserID, user.ID)

	resp.Success(c, gin.H{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func Login(c *gin.Context) {
	var req service.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	loginResp, err := userService.Login(&req)
	if err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}
	c.Set(middleware.ContextKeyUserID, loginResp.User.ID)
	c.Set(middleware.ContextKeyUserRole, string(loginResp.User.Role))

	resp.Success(c, gin.H{
		"token": loginResp.Token,
		"user": gin.H{
			"id":       loginResp.User.ID,
			"username": loginResp.User.Username,
			"role":     loginResp.User.Role,
			"balance":  loginResp.User.Balance,
		},
	})
}

func Logout(c *gin.Context) {
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		resp.Success(c, gin.H{"logged_out": true})
		return
	}
	if claims, err := auth.ParseToken(parts[1]); err == nil {
		c.Set(middleware.ContextKeyUserID, claims.UserID)
		c.Set(middleware.ContextKeyUserRole, claims.Role)
	}

	if err := userService.Logout(parts[1]); err != nil {
		logger.Error("logout error: " + err.Error())
	}

	resp.Success(c, gin.H{"logged_out": true})
}
