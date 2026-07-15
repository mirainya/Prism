package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mirainya/Prism/pkg/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

const (
	// 登录 token 前缀
	LoginTokenPrefix = "login:token:"
	// 用户登录 token 集合前缀，用于角色、状态或密码变更后撤销全部会话。
	LoginUserTokensPrefix = "login:user:"
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
	userTokensKey := loginUserTokensKey(userID)
	_, err := Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, key, userID, LoginTokenExpiration)
		pipe.SAdd(ctx, userTokensKey, token)
		pipe.Expire(ctx, userTokensKey, LoginTokenExpiration)
		return nil
	})
	return err
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
	userID, err := GetLoginToken(ctx, token)
	if err != nil && !errorsIsRedisNil(err) {
		return err
	}
	_, err = Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		if userID > 0 {
			pipe.SRem(ctx, loginUserTokensKey(userID), token)
		}
		return nil
	})
	return err
}

// DeleteUserLoginTokens revokes every active console session for a user.
func DeleteUserLoginTokens(ctx context.Context, userID uint) error {
	if userID == 0 {
		return nil
	}
	setKey := loginUserTokensKey(userID)
	tokens, err := Client.SMembers(ctx, setKey).Result()
	if err != nil && !errorsIsRedisNil(err) {
		return err
	}
	keys := make([]string, 0, len(tokens)+1)
	for _, token := range tokens {
		keys = append(keys, LoginTokenPrefix+token)
	}
	keys = append(keys, setKey)
	return Client.Del(ctx, keys...).Err()
}

// RefreshLoginToken 刷新登录 token 过期时间
func RefreshLoginToken(ctx context.Context, token string) error {
	key := LoginTokenPrefix + token
	userID, err := GetLoginToken(ctx, token)
	if err != nil {
		return err
	}
	_, err = Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Expire(ctx, key, LoginTokenExpiration)
		pipe.Expire(ctx, loginUserTokensKey(userID), LoginTokenExpiration)
		return nil
	})
	return err
}

func loginUserTokensKey(userID uint) string {
	return LoginUserTokensPrefix + strconv.FormatUint(uint64(userID), 10)
}

func errorsIsRedisNil(err error) bool {
	return err == redis.Nil
}
