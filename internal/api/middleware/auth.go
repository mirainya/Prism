package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/errors"
)

const (
	ContextKeyTokenID = "token_id"
	ContextKeyToken   = "token"

	// Token 缓存前缀和 TTL
	tokenCachePrefix = "auth:token:"
	tokenCacheTTL    = 5 * time.Minute
)

// HashTokenKey 对 API Key 做 SHA256 hash
func HashTokenKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// KeyHint 取密钥前缀作为提示（如 sk-prism-ab12...）
func KeyHint(key string) string {
	if len(key) <= 16 {
		return key[:4] + "..."
	}
	return key[:16] + "..."
}

// InvalidateTokenCache 主动失效 Token 缓存（Token 变更时调用）
func InvalidateTokenCache(keyHash string) {
	if cache.Client != nil {
		cache.Client.Del(context.Background(), tokenCachePrefix+keyHash)
	}
}

// InvalidateTokenCacheByID 通过 Token ID 失效缓存（先查 key hash 再删）
func InvalidateTokenCacheByID(tokenID uint) {
	var token model.Token
	if err := model.DB().Select("`key`").Where("id = ?", tokenID).First(&token).Error; err == nil {
		InvalidateTokenCache(token.Key)
	}
}

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

		keyHash := HashTokenKey(tokenKey)

		// 优先从 Redis 缓存读取
		token, err := getTokenFromCache(c.Request.Context(), keyHash)
		if err != nil {
			// 缓存未命中，查数据库
			token = &model.Token{}
			if err := model.DB().Model(&model.Token{}).Where("`key` = ? AND status = 1", keyHash).First(token).Error; err != nil {
				writeAuthenticationError(c, errors.ErrInvalidToken.Message)
				c.Abort()
				return
			}
			// 写入缓存
			setTokenToCache(c.Request.Context(), keyHash, token)
		}

		// 缓存命中但 Token 已禁用
		if token.Status != 1 {
			writeAuthenticationError(c, errors.ErrInvalidToken.Message)
			c.Abort()
			return
		}

		c.Set(ContextKeyTokenID, token.ID)
		c.Set(ContextKeyToken, token)
		c.Next()
	}
}

func getTokenFromCache(ctx context.Context, keyHash string) (*model.Token, error) {
	if cache.Client == nil {
		return nil, fmt.Errorf("cache not initialized")
	}
	data, err := cache.Client.Get(ctx, tokenCachePrefix+keyHash).Bytes()
	if err != nil {
		return nil, err
	}
	var token model.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func setTokenToCache(ctx context.Context, keyHash string, token *model.Token) {
	if cache.Client == nil {
		return
	}
	data, err := json.Marshal(token)
	if err != nil {
		return
	}
	cache.Client.Set(ctx, tokenCachePrefix+keyHash, data, tokenCacheTTL)
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
