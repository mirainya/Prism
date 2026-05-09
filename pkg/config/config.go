package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Redis       RedisConfig       `mapstructure:"redis"`
	Storage     StorageConfig     `mapstructure:"storage"`
	Worker      WorkerConfig      `mapstructure:"worker"`
	HTTPClient  HTTPClientConfig  `mapstructure:"http_client"`
	RateLimit   RateLimitConfig   `mapstructure:"rate_limit"`
	FileStorage FileStorageConfig `mapstructure:"file_storage"`
}

type ServerConfig struct {
	Port      int    `mapstructure:"port"`
	JWTSecret string `mapstructure:"jwt_secret"`
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

type StorageConfig struct {
	Type    string           `mapstructure:"type"`
	Tencent TencentCOSConfig `mapstructure:"tencent"`
}

type TencentCOSConfig struct {
	SecretID  string `mapstructure:"secret_id"`
	SecretKey string `mapstructure:"secret_key"`
	Region    string `mapstructure:"region"`
	Bucket    string `mapstructure:"bucket"`
	CDN       string `mapstructure:"cdn"`
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
	BaseURL       string   `mapstructure:"base_url"`
	APIKey        string   `mapstructure:"api_key"`
	UploadPath    string   `mapstructure:"upload_path"`
	MaxFileSizeMB int      `mapstructure:"max_file_size_mb"`
	AllowedTypes  []string `mapstructure:"allowed_types"`
}

var C *Config

func Load(path string) error {
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return err
	}
	C = &Config{}
	return viper.Unmarshal(C)
}
