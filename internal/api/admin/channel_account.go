package admin

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

// maskAPIKey 脱敏 API Key，保留最后 4 位
func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// ========== ChannelAccount Handlers ==========

func ListChannelAccounts(c *gin.Context) {
	channelID, _ := resp.ParseOptionalUintQuery(c, "channel_id")

	accounts, err := channelService.ListChannelAccounts(channelID)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	result := make([]gin.H, len(accounts))
	for i, acc := range accounts {
		result[i] = gin.H{
			"id":            acc.ID,
			"channel_id":    acc.ChannelID,
			"name":          acc.Name,
			"api_key":       acc.APIKey,
			"masked_key":    maskAPIKey(acc.APIKey),
			"config":        acc.Config,
			"weight":        acc.Weight,
			"status":        acc.Status,
			"current_tasks": acc.CurrentTasks,
			"created_at":    acc.CreatedAt,
			"updated_at":    acc.UpdatedAt,
		}
	}

	resp.Success(c, result)
}

func GetChannelAccount(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	account, err := channelService.GetChannelAccountByID(id)
	if err != nil {
		if errors.Is(err, service.ErrChannelAccountNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{
		"id":            account.ID,
		"channel_id":    account.ChannelID,
		"name":          account.Name,
		"api_key":       account.APIKey,
		"masked_key":    maskAPIKey(account.APIKey),
		"config":        account.Config,
		"weight":        account.Weight,
		"status":        account.Status,
		"current_tasks": account.CurrentTasks,
		"created_at":    account.CreatedAt,
		"updated_at":    account.UpdatedAt,
	})
}

func CreateChannelAccount(c *gin.Context) {
	var req service.CreateChannelAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}

	account, err := channelService.CreateChannelAccount(&req)
	if err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "channel not found"))
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{
		"id":         account.ID,
		"channel_id": account.ChannelID,
		"name":       account.Name,
		"weight":     account.Weight,
		"status":     account.Status,
	})
}

func UpdateChannelAccount(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	var req service.UpdateChannelAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}

	if err := channelService.UpdateChannelAccount(id, &req); err != nil {
		if errors.Is(err, service.ErrChannelAccountNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

func DeleteChannelAccount(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	if err := channelService.DeleteChannelAccount(id); err != nil {
		if errors.Is(err, service.ErrChannelAccountNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"deleted": true})
}
