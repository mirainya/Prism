package config

import (
	"path/filepath"
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

func TestShouldResetGatewayConcurrency(t *testing.T) {
	if !((&Config{}).Server.ShouldResetGatewayConcurrency()) {
		t.Fatal("single-instance default must reset stale concurrency")
	}
	disabled := false
	server := ServerConfig{ResetGatewayConcurrency: &disabled}
	if server.ShouldResetGatewayConcurrency() {
		t.Fatal("explicit multi-instance setting was ignored")
	}
}
