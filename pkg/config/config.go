package config

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

const (
	DefaultFileStorageMaxTotalSizeMB  = 1024
	DefaultFileStorageMaxResultSizeMB = 1024
)

// MinJWTSecretBytes is the minimum entropy budget accepted for the console
// signing key. The configuration loader keeps parsing examples usable for
// documentation, while the runtime validator below fails closed before the
// server starts.
const MinJWTSecretBytes = 32

var ErrInvalidJWTSecret = errors.New("server.jwt_secret must be a strong, non-default secret")

const (
	DefaultAPICallPayloadRetentionHours = 24 * 7
	DefaultAPICallPayloadMaxBytes       = 256 * 1024
	DefaultAPICallMetadataRetentionDays = 90
	DefaultResourceHistoryRetentionDays = 90
	DefaultAPIAccessLogRetentionDays    = 30
	DefaultAuditEventRetentionDays      = 180
	DefaultBillingLedgerRetentionDays   = 365
)

func APICallPayloadRetentionDuration() time.Duration {
	hours := DefaultAPICallPayloadRetentionHours
	if C != nil && C.Observability.APICallPayloadRetentionHours > 0 {
		hours = C.Observability.APICallPayloadRetentionHours
	}
	return time.Duration(hours) * time.Hour
}

func ResourceHistoryRetentionDuration() time.Duration {
	days := DefaultResourceHistoryRetentionDays
	if C != nil && C.Observability.ResourceHistoryRetentionDays > 0 {
		days = C.Observability.ResourceHistoryRetentionDays
	}
	return time.Duration(days) * 24 * time.Hour
}

type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Redis         RedisConfig         `mapstructure:"redis"`
	HTTPClient    HTTPClientConfig    `mapstructure:"http_client"`
	RateLimit     RateLimitConfig     `mapstructure:"rate_limit"`
	FileStorage   FileStorageConfig   `mapstructure:"file_storage"`
	Observability ObservabilityConfig `mapstructure:"observability"`
}

type ServerConfig struct {
	Port      int    `mapstructure:"port"`
	JWTSecret string `mapstructure:"jwt_secret"`
	PublicURL string `mapstructure:"public_url"`
}

// ValidateJWTSecret validates the deployment-time JWT signing secret. It is
// intentionally independent of token generation so unit tests and migration
// tools can construct an in-memory config without weakening the startup gate.
func ValidateJWTSecret(secret string) error {
	value := strings.TrimSpace(secret)
	if value != secret || !utf8.ValidString(value) || len([]byte(value)) < MinJWTSecretBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ErrInvalidJWTSecret
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"your-secret",
		"your_secret",
		"yoursecret",
		"your secret",
		"change-this",
		"change_this",
		"changethis",
		"change this",
		"replace-me",
		"replace_me",
		"replaceme",
		"replace me",
		"default",
		"example",
		"password",
	} {
		if strings.Contains(lower, marker) {
			return ErrInvalidJWTSecret
		}
	}
	// A long repetition is not a usable secret, even though it satisfies the
	// byte-length check (for example, strings.Repeat("a", 32)).
	runes := []rune(value)
	if len(runes) > 0 {
		allSame := true
		for _, r := range runes[1:] {
			if r != runes[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return ErrInvalidJWTSecret
		}
	}
	// Reject short periodic and monotonic patterns commonly used as
	// placeholders. This is deliberately conservative; ordinary generated
	// base64/hex secrets do not match these patterns.
	bytesValue := []byte(value)
	unique := make(map[byte]struct{}, len(bytesValue))
	for _, b := range bytesValue {
		unique[b] = struct{}{}
	}
	if len(unique) < 8 {
		return ErrInvalidJWTSecret
	}
	for period := 1; period <= 16 && period*2 <= len(bytesValue); period++ {
		repeated := true
		for i := period; i < len(bytesValue); i++ {
			if bytesValue[i] != bytesValue[i%period] {
				repeated = false
				break
			}
		}
		if repeated {
			return ErrInvalidJWTSecret
		}
	}
	if len(bytesValue) >= 3 {
		ascending, descending := true, true
		for i := 1; i < len(bytesValue); i++ {
			ascending = ascending && bytesValue[i] == bytesValue[i-1]+1
			descending = descending && bytesValue[i]+1 == bytesValue[i-1]
		}
		if ascending || descending {
			return ErrInvalidJWTSecret
		}
	}
	return nil
}

// Validate checks the values that are security-critical before starting the
// HTTP server. Other fields retain their existing defaults and validation
// paths, so this method deliberately stays narrow.
func (c *Config) Validate() error {
	if c == nil {
		return ErrInvalidJWTSecret
	}
	if err := ValidateJWTSecret(c.Server.JWTSecret); err != nil {
		return fmt.Errorf("%w: invalid server.jwt_secret", err)
	}
	return nil
}

// ValidateRuntime validates the currently loaded process configuration.
func ValidateRuntime() error {
	mu.RLock()
	cfg := C
	mu.RUnlock()
	if cfg == nil {
		return ErrInvalidJWTSecret
	}
	return cfg.Validate()
}

type DatabaseConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	DBName          string `mapstructure:"dbname"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
	ConnMaxIdleTime int    `mapstructure:"conn_max_idle_time"`
	LogLevel        string `mapstructure:"log_level"`
}

type RedisConfig struct {
	Addr         string `mapstructure:"addr"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	PoolSize     int    `mapstructure:"pool_size"`
	MinIdleConns int    `mapstructure:"min_idle_conns"`
	DialTimeout  int    `mapstructure:"dial_timeout"`
	ReadTimeout  int    `mapstructure:"read_timeout"`
	WriteTimeout int    `mapstructure:"write_timeout"`
}

type HTTPClientConfig struct {
	Timeout             int `mapstructure:"timeout"`
	MaxIdleConns        int `mapstructure:"max_idle_conns"`
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
	IdleConnTimeout     int `mapstructure:"idle_conn_timeout"`
}

type RateLimitConfig struct {
	Enabled        bool `mapstructure:"enabled"`
	RequestsPerMin int  `mapstructure:"requests_per_min"`
}

type FileStorageConfig struct {
	BaseURL             string   `mapstructure:"base_url"`
	APIKey              string   `mapstructure:"api_key"`
	UploadPath          string   `mapstructure:"upload_path"`
	MaxFileSizeMB       int      `mapstructure:"max_file_size_mb"`
	MaxResultFileSizeMB int      `mapstructure:"max_result_file_size_mb"`
	MaxTotalSizeMB      int      `mapstructure:"max_total_size_mb"`
	AllowedTypes        []string `mapstructure:"allowed_types"`
}

func FileStorageMaxResultSizeBytes() int64 {
	maximumMB := DefaultFileStorageMaxResultSizeMB
	if cfg := Get(); cfg != nil && cfg.FileStorage.MaxResultFileSizeMB > 0 {
		maximumMB = cfg.FileStorage.MaxResultFileSizeMB
	}
	return int64(maximumMB) * 1024 * 1024
}

type ObservabilityConfig struct {
	RetainAPICallPayloads        bool `mapstructure:"retain_api_call_payloads"`
	APICallPayloadRetentionHours int  `mapstructure:"api_call_payload_retention_hours"`
	APICallPayloadMaxBytes       int  `mapstructure:"api_call_payload_max_bytes"`
	APICallMetadataRetentionDays int  `mapstructure:"api_call_metadata_retention_days"`
	ResourceHistoryRetentionDays int  `mapstructure:"resource_history_retention_days"`
	APIAccessLogRetentionDays    int  `mapstructure:"api_access_log_retention_days"`
	AuditEventRetentionDays      int  `mapstructure:"audit_event_retention_days"`
	BillingLedgerRetentionDays   int  `mapstructure:"billing_ledger_retention_days"`
}

var (
	C         *Config
	mu        sync.RWMutex
	callbacks []func(*Config)
)

func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return C
}

func OnReload(fn func(*Config)) {
	mu.Lock()
	defer mu.Unlock()
	callbacks = append(callbacks, fn)
}

func Load(path string) error {
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return err
	}
	newCfg := &Config{}
	if err := viper.Unmarshal(newCfg); err != nil {
		return err
	}
	applyDefaults(newCfg)

	// 完整解析并应用默认值后一次替换指针，读取方不会看到半更新配置。
	mu.Lock()
	C = newCfg
	mu.Unlock()
	return nil
}

func Watch() {
	viper.OnConfigChange(func(e fsnotify.Event) {
		newCfg := &Config{}
		if err := viper.Unmarshal(newCfg); err != nil {
			return
		}
		applyDefaults(newCfg)
		// A reload must not replace a known-good signing key with an example or
		// weak value. Keep the previous configuration active until the file is
		// corrected and another change event arrives.
		if err := newCfg.Validate(); err != nil {
			return
		}

		// 回调在锁外执行，避免回调再次读取配置或注册监听时发生死锁。
		mu.Lock()
		C = newCfg
		cbs := make([]func(*Config), len(callbacks))
		copy(cbs, callbacks)
		mu.Unlock()
		for _, fn := range cbs {
			fn(newCfg)
		}
	})
	viper.WatchConfig()
}

func applyDefaults(cfg *Config) {
	if cfg.FileStorage.MaxResultFileSizeMB <= 0 {
		cfg.FileStorage.MaxResultFileSizeMB = DefaultFileStorageMaxResultSizeMB
	}
	if cfg.FileStorage.MaxTotalSizeMB <= 0 {
		cfg.FileStorage.MaxTotalSizeMB = DefaultFileStorageMaxTotalSizeMB
	}
	if cfg.Observability.APICallPayloadRetentionHours <= 0 {
		cfg.Observability.APICallPayloadRetentionHours = DefaultAPICallPayloadRetentionHours
	}
	if cfg.Observability.APICallPayloadMaxBytes <= 0 {
		cfg.Observability.APICallPayloadMaxBytes = DefaultAPICallPayloadMaxBytes
	}
	if cfg.Observability.APICallMetadataRetentionDays <= 0 {
		cfg.Observability.APICallMetadataRetentionDays = DefaultAPICallMetadataRetentionDays
	}
	if cfg.Observability.ResourceHistoryRetentionDays <= 0 {
		cfg.Observability.ResourceHistoryRetentionDays = DefaultResourceHistoryRetentionDays
	}
	if cfg.Observability.APIAccessLogRetentionDays <= 0 {
		cfg.Observability.APIAccessLogRetentionDays = DefaultAPIAccessLogRetentionDays
	}
	if cfg.Observability.AuditEventRetentionDays <= 0 {
		cfg.Observability.AuditEventRetentionDays = DefaultAuditEventRetentionDays
	}
	if cfg.Observability.BillingLedgerRetentionDays <= 0 {
		cfg.Observability.BillingLedgerRetentionDays = DefaultBillingLedgerRetentionDays
	}
}
