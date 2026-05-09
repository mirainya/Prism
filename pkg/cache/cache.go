package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/mirainya/Prism/pkg/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

const (
	// 登录 token 前缀
	LoginTokenPrefix = "login:token:"
	// 登录 token 默认过期时间 24 小时
	LoginTokenExpiration = 24 * time.Hour
)

// Init 初始化 Redis 客户端
func Init() error {
	cfg := config.C.Redis
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 20
	}
	minIdleConns := cfg.MinIdleConns
	if minIdleConns <= 0 {
		minIdleConns = 5
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5
	}
	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 3
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 3
	}

	Client = redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     poolSize,
		MinIdleConns: minIdleConns,
		DialTimeout:  time.Duration(dialTimeout) * time.Second,
		ReadTimeout:  time.Duration(readTimeout) * time.Second,
		WriteTimeout: time.Duration(writeTimeout) * time.Second,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}

	return nil
}

// Close 关闭 Redis 连接
func Close() error {
	if Client != nil {
		return Client.Close()
	}
	return nil
}

// SetLoginToken 存储登录 token
func SetLoginToken(ctx context.Context, token string, userID uint) error {
	key := LoginTokenPrefix + token
	return Client.Set(ctx, key, userID, LoginTokenExpiration).Err()
}

// GetLoginToken 获取登录 token 对应的用户 ID
func GetLoginToken(ctx context.Context, token string) (uint, error) {
	key := LoginTokenPrefix + token
	result, err := Client.Get(ctx, key).Uint64()
	if err != nil {
		return 0, err
	}
	return uint(result), nil
}

// DeleteLoginToken 删除登录 token (登出)
func DeleteLoginToken(ctx context.Context, token string) error {
	key := LoginTokenPrefix + token
	return Client.Del(ctx, key).Err()
}

// RefreshLoginToken 刷新登录 token 过期时间
func RefreshLoginToken(ctx context.Context, token string) error {
	key := LoginTokenPrefix + token
	return Client.Expire(ctx, key, LoginTokenExpiration).Err()
}
