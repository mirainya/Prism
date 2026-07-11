package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	CallbackChannelContextKey = "callback_channel"
	CallbackTaskContextKey    = "callback_task"
)

// CallbackAuth authenticates the task-scoped callback URL before the provider
// body is parsed. Providers only need to call the exact URL sent at submit time.
func CallbackAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		channelType := c.Param("channel_type")
		taskNo := c.Param("task_no")
		signature := c.Param("signature")

		var channel model.Channel
		if err := model.DB().Where("type = ?", channelType).First(&channel).Error; err != nil {
			handleCallbackLookupError(c, "channel", err)
			return
		}
		if channel.CallbackSecret == "" ||
			!auth.VerifyCallbackSignature(channel.CallbackSecret, channel.ID, taskNo, signature) {
			logger.Warn("callback URL signature verification failed",
				zap.String("channel_type", channelType),
				zap.String("task_no", taskNo))
			forbidCallback(c)
			return
		}

		var task model.Task
		if err := model.DB().Where("task_no = ? AND channel_id = ?", taskNo, channel.ID).
			First(&task).Error; err != nil {
			handleCallbackLookupError(c, "task", err)
			return
		}

		c.Set(CallbackChannelContextKey, &channel)
		c.Set(CallbackTaskContextKey, &task)
		c.Next()
	}
}

func forbidCallback(c *gin.Context) {
	resp.ErrorMsg(c, http.StatusForbidden, http.StatusForbidden, "invalid callback authentication")
	c.Abort()
}

func handleCallbackLookupError(c *gin.Context, resource string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		forbidCallback(c)
		return
	}
	logger.Error("callback authentication lookup failed",
		zap.String("resource", resource),
		zap.Error(err))
	resp.ErrorMsg(c, http.StatusInternalServerError, http.StatusInternalServerError, "callback authentication failed")
	c.Abort()
}
