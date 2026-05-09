package console

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var tokenService = service.NewTokenService()

func ListMyTokens(c *gin.Context) {
	userID := middleware.GetUserID(c)
	result, err := tokenService.ListTokens(userID)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	resp.Success(c, result)
}

func CreateToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req service.CreateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	result, err := tokenService.CreateToken(userID, &req)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, result)
}

func GetToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	tokenID := c.Param("id")

	var id uint
	if _, err := fmt.Sscanf(tokenID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid token id"))
		return
	}

	result, err := tokenService.GetToken(userID, id)
	if err != nil {
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}

	resp.Success(c, result)
}

func UpdateToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	tokenID := c.Param("id")

	var id uint
	if _, err := fmt.Sscanf(tokenID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid token id"))
		return
	}

	var req service.UpdateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	if err := tokenService.UpdateToken(userID, id, &req); err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

func DeleteToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	tokenID := c.Param("id")

	var id uint
	if _, err := fmt.Sscanf(tokenID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid token id"))
		return
	}

	if err := tokenService.DeleteToken(userID, id); err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"deleted": true})
}

type RechargeTokenRequest struct {
	Amount decimal.Decimal `json:"amount" binding:"required"`
}

func RechargeToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	tokenID := c.Param("id")

	var id uint
	if _, err := fmt.Sscanf(tokenID, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid token id"))
		return
	}

	var req RechargeTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	token, err := tokenService.RechargeToken(userID, id, req.Amount)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{
		"id":      token.ID,
		"balance": token.Balance,
	})
}
