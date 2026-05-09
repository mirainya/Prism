package admin

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/shopspring/decimal"
)

var userService = service.NewUserService()

func ListUsers(c *gin.Context) {
	users, err := userService.ListUsers()
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	result := make([]gin.H, len(users))
	for i, u := range users {
		result[i] = gin.H{
			"id":         u.ID,
			"username":   u.Username,
			"role":       u.Role,
			"balance":    u.Balance,
			"status":     u.Status,
			"created_at": u.CreatedAt,
		}
	}

	resp.Success(c, result)
}

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=admin user"`
}

func UpdateUserRole(c *gin.Context) {
	userID := c.Param("id")

	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	var id uint
	if _, err := fmt.Sscanf(userID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid user id"))
		return
	}

	if err := userService.UpdateUserRole(id, model.UserRole(req.Role)); err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

type UpdateStatusRequest struct {
	Status int8 `json:"status" binding:"oneof=0 1"`
}

func UpdateUserStatus(c *gin.Context) {
	userID := c.Param("id")

	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	var id uint
	if _, err := fmt.Sscanf(userID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid user id"))
		return
	}

	if err := userService.UpdateUserStatus(id, req.Status); err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

type RechargeRequest struct {
	Amount decimal.Decimal `json:"amount" binding:"required"`
}

func RechargeUser(c *gin.Context) {
	userID := c.Param("id")

	var req RechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	var id uint
	if _, err := fmt.Sscanf(userID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid user id"))
		return
	}

	if err := userService.RechargeUser(id, req.Amount); err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"recharged": true})
}
