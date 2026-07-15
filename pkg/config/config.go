package config

import (
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

const DefaultFileStorageMaxTotalSizeMB = 1024

const (
	DefaultAPICallPayloadRetentionHours = 24 * 7
	DefaultAPICallPayloadMaxBytes       = 256 * 1024
	DefaultAPICallMetadataRetentionDays = 90
	DefaultResourceHistoryRetentionDays = 90
	DefaultAPIAccessLogRetentionDays    = 30
	DefaultAuditEventRetentionDays      = 180
	DefaultBillingLedgerRetentionDays   = 365
)

type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Redis         RedisConfig         `mapstructure:"redis"`
	Worker        WorkerConfig        `mapstructure:"worker"`
	HTTPClient    HTTPClientConfig    `mapstructure:"http_client"`
	RateLimit     RateLimitConfig     `mapstructure:"rate_limit"`
	FileStorage   FileStorageConfig   `mapstructure:"file_storage"`
	Observability ObservabilityConfig `mapstructure:"observability"`
}

type ServerConfig struct {
	Port                    int    `mapstructure:"port"`
	JWTSecret               string `mapstructure:"jwt_secret"`
	PublicURL               string `mapstructure:"public_url"`
	ResetGatewayConcurrency *bool  `mapstructure:"reset_gateway_concurrency_on_start"`
}

func (c ServerConfig) ShouldResetGatewayConcurrency() bool {
	return c.ResetGatewayConcurrency == nil || *c.ResetGatewayConcurrency
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

type WorkerConfig struct {
	Concurrency  int    `mapstructure:"concurrency"`
	PollInterval string `mapstructure:"poll_interval"`
	MaxRetry     int    `mapstructure:"max_retry"`
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
	BaseURL        string   `mapstructure:"base_url"`
	APIKey         string   `mapstructure:"api_key"`
	UploadPath     string   `mapstructure:"upload_path"`
	MaxFileSizeMB  int      `mapstructure:"max_file_size_mb"`
	MaxTotalSizeMB int      `mapstructure:"max_total_size_mb"`
	AllowedTypes   []string `mapstructure:"allowed_types"`
}

type ObservabilityConfig struct {
	RetainAPICallPayloads        bool   `mapstructure:"retain_api_call_payloads"`
	APICallPayloadRetentionHours int    `mapstructure:"api_call_payload_retention_hours"`
	APICallPayloadMaxBytes       int    `mapstructure:"api_call_payload_max_bytes"`
	APICallPayloadEncryptionKey  string `mapstructure:"api_call_payload_encryption_key"`
	APICallMetadataRetentionDays int    `mapstructure:"api_call_metadata_retention_days"`
	ResourceHistoryRetentionDays int    `mapstructure:"resource_history_retention_days"`
	APIAccessLogRetentionDays    int    `mapstructure:"api_access_log_retention_days"`
	AuditEventRetentionDays      int    `mapstructure:"audit_event_retention_days"`
	BillingLedgerRetentionDays   int    `mapstructure:"billing_ledger_retention_days"`
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
