package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestSampleConfigSetsFileStorageQuota(t *testing.T) {
	previous := Get()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		mu.Lock()
		C = previous
		mu.Unlock()
	})

	for _, filename := range []string{"config.example.yaml", "config.docker.yaml"} {
		path := filepath.Join("..", "..", "configs", filename)
		if err := Load(path); err != nil {
			t.Fatalf("load %s: %v", filename, err)
		}
		if got := Get().FileStorage.MaxTotalSizeMB; got != DefaultFileStorageMaxTotalSizeMB {
			t.Fatalf("%s max total size = %d, want %d", filename, got, DefaultFileStorageMaxTotalSizeMB)
		}
	}
}

func TestApplyDefaultsSetsFileStorageQuota(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.FileStorage.MaxTotalSizeMB != DefaultFileStorageMaxTotalSizeMB {
		t.Fatalf("max total size = %d, want %d", cfg.FileStorage.MaxTotalSizeMB, DefaultFileStorageMaxTotalSizeMB)
	}
}

func TestApplyDefaultsPreservesConfiguredFileQuota(t *testing.T) {
	cfg := &Config{FileStorage: FileStorageConfig{MaxTotalSizeMB: 2048}}
	applyDefaults(cfg)
	if cfg.FileStorage.MaxTotalSizeMB != 2048 {
		t.Fatalf("max total size = %d, want 2048", cfg.FileStorage.MaxTotalSizeMB)
	}
}

func TestValidateJWTSecretRejectsDefaultsAndWeakValues(t *testing.T) {
	for _, value := range []string{
		"",
		" strong-secret-value-that-is-long-enough-but-has-padding ",
		"short-secret",
		"your-secret-key-change-in-production",
		"change-this-to-a-secure-random-string-but-still-a-default",
		strings.Repeat("a", MinJWTSecretBytes),
		strings.Repeat("ab", MinJWTSecretBytes/2),
		strings.Repeat("a", MinJWTSecretBytes-1) + "b",
		"01234567890123456789012345678901",
		string([]byte{0xff, 0xfe}) + strings.Repeat("x", MinJWTSecretBytes),
	} {
		if err := ValidateJWTSecret(value); !errors.Is(err, ErrInvalidJWTSecret) {
			t.Fatalf("secret %q error=%v, want ErrInvalidJWTSecret", value, err)
		}
	}
}

func TestValidateJWTSecretAcceptsStrongConfiguredValue(t *testing.T) {
	secret := "m3L!qP7#vR2@xK9$zN4%tY8^bC6&hJ1*"
	if err := ValidateJWTSecret(secret); err != nil {
		t.Fatalf("strong secret rejected: %v", err)
	}
}

func TestConfigValidateAndRuntimeValidation(t *testing.T) {
	if err := (&Config{Server: ServerConfig{JWTSecret: "short"}}).Validate(); !errors.Is(err, ErrInvalidJWTSecret) {
		t.Fatalf("weak config error=%v, want ErrInvalidJWTSecret", err)
	}
	previous := Get()
	mu.Lock()
	C = &Config{Server: ServerConfig{JWTSecret: "m3L!qP7#vR2@xK9$zN4%tY8^bC6&hJ1*"}}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		C = previous
		mu.Unlock()
	})
	if err := ValidateRuntime(); err != nil {
		t.Fatalf("runtime validation rejected strong config: %v", err)
	}
}
