package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"github.com/mirainya/Prism/pkg/errors"
)

const (
	ContextKeyTokenID = "token_id"
	ContextKeyToken   = "token"
)

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenKey := strings.TrimSpace(c.GetHeader("Authorization"))
		if tokenKey == "" {
			tokenKey = strings.TrimSpace(c.GetHeader("x-api-key"))
		}
		if tokenKey == "" {
			writeAuthenticationError(c, "missing API key")
			c.Abort()
			return
		}

		// 支持 Bearer 前缀
		if len(tokenKey) >= len("Bearer ") && strings.EqualFold(tokenKey[:len("Bearer ")], "Bearer ") {
			tokenKey = strings.TrimSpace(tokenKey[len("Bearer "):])
		}
		if tokenKey == "" {
			writeAuthenticationError(c, "missing API key")
			c.Abort()
			return
		}

		selector, secret, credentialVersion, err := tokenauth.Resolve(tokenKey)
		if err != nil {
			writeAuthenticationError(c, errors.ErrInvalidToken.Message)
			c.Abort()
			return
		}

		token := &model.Token{}
		err = model.DB().WithContext(c.Request.Context()).Table("tokens AS token").
			Select("token.*").
			Joins("JOIN users AS owner ON owner.id=token.user_id AND owner.deleted_at IS NULL").
			Where("token.selector=? AND token.status=1 AND token.revoked_at IS NULL", selector).
			Where("token.auth_version>0 AND owner.status=1").
			Where("token.deleted_at IS NULL AND (token.expires_at IS NULL OR token.expires_at>?)", time.Now().UTC()).
			Take(token).Error
		if err != nil || credentialVersion != token.SecretDigestVersion || !tokenauth.Verify(selector, secret, token.SecretDigestVersion, token.SecretDigest) {
			writeAuthenticationError(c, errors.ErrInvalidToken.Message)
			c.Abort()
			return
		}

		c.Set(ContextKeyTokenID, token.ID)
		c.Set(ContextKeyToken, token)
		c.Next()
	}
}

func GetTokenID(c *gin.Context) uint {
	if v, exists := c.Get(ContextKeyTokenID); exists {
		if id, ok := v.(uint); ok {
			return id
		}
	}
	return 0
}

func GetToken(c *gin.Context) *model.Token {
	if v, exists := c.Get(ContextKeyToken); exists {
		if token, ok := v.(*model.Token); ok {
			return token
		}
	}
	return nil
}
