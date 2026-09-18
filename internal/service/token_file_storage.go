package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/filestorage"
	"gorm.io/gorm"
)

var (
	ErrInvalidFileStorageAPIKey  = errors.New("invalid XFileStorage API key")
	ErrFileStorageProbeFailed    = errors.New("XFileStorage verification failed")
	ErrFileStorageUpdateConflict = errors.New("XFileStorage setting changed concurrently")
)

var xfsAPIKeyPattern = regexp.MustCompile(`^xfs_[0-9a-f]{32}$`)

const tokenFileStorageProbeTimeout = 30 * time.Second

type TokenFileStorageStatus struct {
	Configured bool   `json:"configured"`
	KeyHint    string `json:"key_hint"`
}

func (s *TokenService) BindFileStorage(ctx context.Context, userID, tokenID uint, apiKey string) (TokenFileStorageStatus, error) {
	apiKey = strings.TrimSpace(apiKey)
	if ctx == nil || userID == 0 || tokenID == 0 || !xfsAPIKeyPattern.MatchString(apiKey) {
		return TokenFileStorageStatus{}, ErrInvalidFileStorageAPIKey
	}
	var token model.Token
	if err := model.DB().WithContext(ctx).Select("id").Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", tokenID, userID).First(&token).Error; err != nil {
		return TokenFileStorageStatus{}, err
	}
	probe := s.probeFileStorage
	if probe == nil {
		probe = probeTokenFileStorage
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, tokenFileStorageProbeTimeout)
	defer cancelProbe()
	if err := probe(probeCtx, apiKey); err != nil {
		return TokenFileStorageStatus{}, fmt.Errorf("%w: %v", ErrFileStorageProbeFailed, err)
	}
	result := model.DB().WithContext(ctx).Model(&model.Token{}).
		Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", tokenID, userID).
		UpdateColumn("xfs_api_key", apiKey)
	if result.Error != nil {
		return TokenFileStorageStatus{}, result.Error
	}
	if result.RowsAffected == 0 {
		if err := confirmTokenFileStorageValue(ctx, userID, tokenID, apiKey); err != nil {
			return TokenFileStorageStatus{}, err
		}
	}
	return newTokenFileStorageStatus(apiKey), nil
}

func (s *TokenService) UnbindFileStorage(ctx context.Context, userID, tokenID uint) (TokenFileStorageStatus, error) {
	if ctx == nil || userID == 0 || tokenID == 0 {
		return TokenFileStorageStatus{}, gorm.ErrRecordNotFound
	}
	var token model.Token
	if err := model.DB().WithContext(ctx).Select("id").Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", tokenID, userID).First(&token).Error; err != nil {
		return TokenFileStorageStatus{}, err
	}
	result := model.DB().WithContext(ctx).Model(&model.Token{}).
		Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", tokenID, userID).
		UpdateColumn("xfs_api_key", "")
	if result.Error != nil {
		return TokenFileStorageStatus{}, result.Error
	}
	if result.RowsAffected == 0 {
		if err := confirmTokenFileStorageValue(ctx, userID, tokenID, ""); err != nil {
			return TokenFileStorageStatus{}, err
		}
	}
	return newTokenFileStorageStatus(""), nil
}

func confirmTokenFileStorageValue(ctx context.Context, userID, tokenID uint, expected string) error {
	var token model.Token
	if err := model.DB().WithContext(ctx).Select("id", "xfs_api_key").Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", tokenID, userID).First(&token).Error; err != nil {
		return err
	}
	if strings.TrimSpace(token.XFSAPIKey) != expected {
		return ErrFileStorageUpdateConflict
	}
	return nil
}

func probeTokenFileStorage(ctx context.Context, apiKey string) error {
	return filestorage.WithAPIKey(apiKey).Probe(ctx)
}

func newTokenFileStorageStatus(apiKey string) TokenFileStorageStatus {
	apiKey = strings.TrimSpace(apiKey)
	return TokenFileStorageStatus{Configured: apiKey != "", KeyHint: fileStorageKeyHint(apiKey)}
}

func fileStorageKeyHint(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) < 4 {
		return "****"
	}
	return "****" + apiKey[len(apiKey)-4:]
}
