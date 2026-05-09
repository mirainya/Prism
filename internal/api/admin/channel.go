package admin

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

var channelService = service.NewChannelService()

// ========== Channel Handlers ==========

func ListChannels(c *gin.Context) {
	channels, err := channelService.ListChannels()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	result := make([]gin.H, len(channels))
	for i, ch := range channels {
		accountCount, _ := channelService.GetChannelAccountCount(ch.ID)
		result[i] = gin.H{
			"id":             ch.ID,
			"type":           ch.Type,
			"name":           ch.Name,
			"base_url":       ch.BaseURL,
			"config":         ch.Config,
			"status":         ch.Status,
			"accounts_count": accountCount,
			"created_at":     ch.CreatedAt,
			"updated_at":     ch.UpdatedAt,
		}
	}

	resp.Success(c, result)
}

func GetChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	channel, err := channelService.GetChannelByID(id)
	if err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	accountCount, _ := channelService.GetChannelAccountCount(channel.ID)

	resp.Success(c, gin.H{
		"id":             channel.ID,
		"type":           channel.Type,
		"name":           channel.Name,
		"base_url":       channel.BaseURL,
		"config":         channel.Config,
		"status":         channel.Status,
		"accounts_count": accountCount,
		"created_at":     channel.CreatedAt,
		"updated_at":     channel.UpdatedAt,
	})
}

func CreateChannel(c *gin.Context) {
	var req service.CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}

	channel, err := channelService.CreateChannel(&req)
	if err != nil {
		if errors.Is(err, service.ErrChannelTypeExists) {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "channel type already exists"))
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{
		"id":       channel.ID,
		"type":     channel.Type,
		"name":     channel.Name,
		"base_url": channel.BaseURL,
		"status":   channel.Status,
	})
}

func UpdateChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	var req service.UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}

	if err := channelService.UpdateChannel(id, &req); err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"updated": true})
}

func DeleteChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}

	if err := channelService.DeleteChannel(id); err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	resp.Success(c, gin.H{"deleted": true})
}
