package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mirainya/Prism/pkg/config"
)

type Claims struct {
	UserID         uint   `json:"user_id"`
	Username       string `json:"username"`
	Role           string `json:"role"`
	SessionVersion uint64 `json:"session_version"`
	jwt.RegisteredClaims
}

var ErrJWTConfiguration = errors.New("jwt signing secret is not configured")

func GenerateToken(userID uint, username string, role string) (string, error) {
	return GenerateTokenWithSessionVersion(userID, username, role, 0)
}

func GenerateTokenWithSessionVersion(userID uint, username string, role string, sessionVersion uint64) (string, error) {
	secret, err := signingSecret()
	if err != nil {
		return "", err
	}
	if userID == 0 || strings.TrimSpace(username) == "" || strings.TrimSpace(role) == "" {
		return "", errors.New("invalid JWT subject")
	}
	claims := Claims{
		UserID:         userID,
		Username:       username,
		Role:           role,
		SessionVersion: sessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "prism",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func ParseToken(tokenString string) (*Claims, error) {
	secret, err := signingSecret()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenString) == "" || len(tokenString) > 8192 {
		return nil, jwt.ErrTokenMalformed
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 || token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid && validClaims(claims) {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
}

func validClaims(claims *Claims) bool {
	if claims == nil || claims.UserID == 0 || strings.TrimSpace(claims.Username) == "" || strings.TrimSpace(claims.Role) == "" || claims.Issuer != "prism" || claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return false
	}
	return claims.ExpiresAt.After(time.Now())
}

func signingSecret() ([]byte, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, ErrJWTConfiguration
	}
	secret := strings.TrimSpace(cfg.Server.JWTSecret)
	if secret == "" {
		return nil, ErrJWTConfiguration
	}
	return []byte(secret), nil
}
