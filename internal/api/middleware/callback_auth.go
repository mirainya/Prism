package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

const CallbackSignatureHeader = "X-Callback-Signature"

// CallbackAuth 回调接口 HMAC 签名验证中间件
func CallbackAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		channelType := c.Param("channel_type")

		// 查找渠道
		var channel model.Channel
		if err := model.DB().Where("type = ? AND status = 1", channelType).First(&channel).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			c.Abort()
			return
		}

		// 如果渠道配置了回调密钥，则验证签名
		if channel.CallbackSecret != "" {
			signature := c.GetHeader(CallbackSignatureHeader)
			if signature == "" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "missing callback signature"})
				c.Abort()
				return
			}

			// 读取 body 并验证签名
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
				c.Abort()
				return
			}
			// 重新设置 body 供后续 handler 使用
			c.Request.Body = io.NopCloser(bytes.NewReader(body))

			if !verifyHMAC(body, signature, channel.CallbackSecret) {
				logger.Warn("callback signature verification failed",
					zap.String("channel_type", channelType))
				c.JSON(http.StatusForbidden, gin.H{"error": "invalid callback signature"})
				c.Abort()
				return
			}
		}

		// 将渠道信息存入 context
		c.Set("callback_channel", &channel)
		c.Next()
	}
}

func verifyHMAC(body []byte, signature string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
