package auth

import (
	"testing"

	"github.com/mirainya/Prism/pkg/config"
)

func TestGenerateTokenCarriesSessionVersion(t *testing.T) {
	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{JWTSecret: "jwt-session-version-test"}}
	t.Cleanup(func() { config.C = previousConfig })

	token, err := GenerateTokenWithSessionVersion(7, "alice", "user", 42)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != 7 || claims.Username != "alice" || claims.Role != "user" || claims.SessionVersion != 42 {
		t.Fatalf("claims=%#v", claims)
	}
}

func TestGenerateTokenKeepsLegacyZeroVersion(t *testing.T) {
	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{JWTSecret: "jwt-legacy-test"}}
	t.Cleanup(func() { config.C = previousConfig })

	token, err := GenerateToken(8, "legacy", "admin")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionVersion != 0 {
		t.Fatalf("session version=%d, want 0", claims.SessionVersion)
	}
}
