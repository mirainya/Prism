package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func TestParseTokenRejectsNonHS256Algorithm(t *testing.T) {
	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{JWTSecret: "jwt-algorithm-test-secret"}}
	t.Cleanup(func() { config.C = previousConfig })
	claims := Claims{
		UserID: 1, Username: "alice", Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "prism",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString([]byte(config.C.Server.JWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(token); err == nil {
		t.Fatal("HS512 token was accepted")
	}
}

func TestParseTokenRejectsDefaultClaims(t *testing.T) {
	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{JWTSecret: "jwt-claims-test-secret"}}
	t.Cleanup(func() { config.C = previousConfig })
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "prism",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.C.Server.JWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(token); !errors.Is(err, jwt.ErrSignatureInvalid) {
		t.Fatalf("zero-subject token error=%v, want signature invalid", err)
	}
}

func TestJWTFunctionsReturnErrorWhenConfigIsMissing(t *testing.T) {
	previousConfig := config.C
	config.C = nil
	t.Cleanup(func() { config.C = previousConfig })
	if _, err := GenerateToken(1, "alice", "admin"); !errors.Is(err, ErrJWTConfiguration) {
		t.Fatalf("GenerateToken error=%v, want ErrJWTConfiguration", err)
	}
	if _, err := ParseToken("not-a-token"); !errors.Is(err, ErrJWTConfiguration) {
		t.Fatalf("ParseToken error=%v, want ErrJWTConfiguration", err)
	}
}
