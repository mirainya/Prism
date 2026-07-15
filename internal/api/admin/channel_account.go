package admin

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

var accountCircuitService = service.NewAccountCircuitService()

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
		states, _ := accountCircuitService.ListByAccount(acc.ID)
		result[i] = channelAccountResponse(&acc, states)
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

	states, _ := accountCircuitService.ListByAccount(account.ID)
	resp.Success(c, channelAccountResponse(account, states))
}

func channelAccountResponse(account *model.ChannelAccount, states []model.AccountModelState) gin.H {
	maskedKey := service.MaskCredential(account.APIKey)
	return gin.H{
		"id":               account.ID,
		"channel_id":       account.ChannelID,
		"name":             account.Name,
		"api_key":          maskedKey,
		"masked_key":       maskedKey,
		"config":           account.Config,
		"weight":           account.Weight,
		"status":           account.Status,
		"max_tasks":        account.MaxTasks,
		"current_tasks":    account.CurrentTasks,
		"supported_models": account.SupportedModels,
		"circuit_states":   states,
		"created_at":       account.CreatedAt,
		"updated_at":       account.UpdatedAt,
	}
}

// ListAccountCircuitStates 列出账号当前生效的熔断状态
func ListAccountCircuitStates(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	states, err := accountCircuitService.ListByAccount(id)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, states)
}

// ClearAccountCircuitState 手动解除账号对某模型的熔断
func ClearAccountCircuitState(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	modelCode := c.Param("model_code")
	if modelCode == "" {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "model_code required"))
		return
	}
	if err := accountCircuitService.Clear(id, modelCode); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"cleared": true})
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
