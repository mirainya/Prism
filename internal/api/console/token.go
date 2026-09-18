package console

import (
	"encoding/json"
	stdErrors "errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var tokenService = service.NewTokenService()

type tokenFileStorageRequest struct {
	APIKey string `json:"api_key"`
}

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
		if stdErrors.Is(err, service.ErrInvalidBalanceAmount) {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, result)
}

func GetToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
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
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	var req service.UpdateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	if err := tokenService.UpdateToken(userID, id, &req); err != nil {
		if stdErrors.Is(err, gorm.ErrRecordNotFound) {
			resp.NotFound(c, errors.WithMessage(errors.ErrTaskNotFound, "token not found"))
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

func DeleteToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
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

func BindTokenFileStorage(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	req, ok := decodeTokenFileStorageBody(c)
	if !ok {
		return
	}
	result, err := tokenService.BindFileStorage(c.Request.Context(), userID, id, req.APIKey)
	if err != nil {
		switch {
		case stdErrors.Is(err, service.ErrInvalidFileStorageAPIKey):
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		case stdErrors.Is(err, service.ErrFileStorageProbeFailed):
			resp.ErrorMsg(c, http.StatusBadGateway, errors.ErrUploadFailed.Code, "file storage verification failed")
		case stdErrors.Is(err, gorm.ErrRecordNotFound):
			resp.NotFound(c, errors.WithMessage(errors.ErrTaskNotFound, "token not found"))
		default:
			resp.InternalError(c, errors.ErrInternalError)
		}
		return
	}
	resp.Success(c, result)
}

func UnbindTokenFileStorage(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	result, err := tokenService.UnbindFileStorage(c.Request.Context(), userID, id)
	if err != nil {
		if stdErrors.Is(err, gorm.ErrRecordNotFound) {
			resp.NotFound(c, errors.WithMessage(errors.ErrTaskNotFound, "token not found"))
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	resp.Success(c, result)
}

func decodeTokenFileStorageBody(c *gin.Context) (*tokenFileStorageRequest, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var req *tokenFileStorageRequest
	if err := decoder.Decode(&req); err != nil || req == nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid request body"))
		return nil, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid request body"))
		return nil, false
	}
	return req, true
}

type RechargeTokenRequest struct {
	Amount decimal.Decimal `json:"amount" binding:"required"`
}

func RechargeToken(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	var req RechargeTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	token, err := tokenService.RechargeToken(userID, id, req.Amount)
	if err != nil {
		if stdErrors.Is(err, service.ErrInvalidBalanceAmount) {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
			return
		}
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{
		"id":         token.ID,
		"balance":    token.Balance,
		"total_used": token.TotalUsed,
	})
}
