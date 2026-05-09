package service

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TokenService struct{}

func NewTokenService() *TokenService {
	return &TokenService{}
}

type ChannelPriorityInput struct {
	CapabilityCode string `json:"capability_code"`
	ChannelID      uint   `json:"channel_id"`
	Priority       int    `json:"priority"`
}

type CreateTokenReq struct {
	Name              string                 `json:"name" binding:"required,max=50"`
	Balance           decimal.Decimal        `json:"balance"`
	ChannelPriorities []ChannelPriorityInput `json:"channel_priorities"`
}

type UpdateTokenReq struct {
	Name              string                 `json:"name" binding:"max=50"`
	ChannelPriorities []ChannelPriorityInput `json:"channel_priorities"`
}

func (s *TokenService) ListTokens(userID uint) ([]gin.H, error) {
	var tokens []model.Token
	if err := model.DB().Model(&model.Token{}).Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}

	tokenIDs := make([]uint, len(tokens))
	for i, t := range tokens {
		tokenIDs[i] = t.ID
	}

	var priorities []model.TokenChannelPriority
	if len(tokenIDs) > 0 {
		model.DB().Where("token_id IN ?", tokenIDs).Order("priority ASC").Find(&priorities)
	}

	priorityMap := make(map[uint][]gin.H)
	for _, p := range priorities {
		priorityMap[p.TokenID] = append(priorityMap[p.TokenID], gin.H{
			"capability_code": p.CapabilityCode,
			"channel_id":      p.ChannelID,
			"priority":        p.Priority,
		})
	}

	result := make([]gin.H, len(tokens))
	for i, t := range tokens {
		result[i] = gin.H{
			"id":                 t.ID,
			"name":               t.Name,
			"key":                t.KeyHint,
			"balance":            t.Balance,
			"total_used":         t.TotalUsed,
			"rate_limit":         t.RateLimit,
			"status":             t.Status,
			"created_at":         t.CreatedAt,
			"channel_priorities": priorityMap[t.ID],
		}
	}

	return result, nil
}

func (s *TokenService) CreateToken(userID uint, req *CreateTokenReq) (gin.H, error) {
	plainKey := generateAPIKey()
	keyHash := middleware.HashTokenKey(plainKey)
	keyHint := middleware.KeyHint(plainKey)

	token := &model.Token{
		UserID:    userID,
		Name:      req.Name,
		Key:       keyHash,
		KeyHint:   keyHint,
		Balance:   req.Balance,
		RateLimit: 60,
		Status:    1,
	}

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(token).Error; err != nil {
			return err
		}
		if len(req.ChannelPriorities) > 0 {
			return saveChannelPriorities(tx, token.ID, req.ChannelPriorities)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return gin.H{
		"id":      token.ID,
		"name":    token.Name,
		"key":     plainKey,
		"balance": token.Balance,
	}, nil
}

func (s *TokenService) GetToken(userID uint, id uint) (gin.H, error) {
	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ?", id, userID).First(&token).Error; err != nil {
		return nil, err
	}

	var priorities []model.TokenChannelPriority
	model.DB().Where("token_id = ?", id).Order("capability_code ASC, priority ASC").Find(&priorities)

	priorityList := make([]gin.H, len(priorities))
	for i, p := range priorities {
		priorityList[i] = gin.H{
			"capability_code": p.CapabilityCode,
			"channel_id":      p.ChannelID,
			"priority":        p.Priority,
		}
	}

	return gin.H{
		"id":                 token.ID,
		"name":               token.Name,
		"key":                token.KeyHint,
		"balance":            token.Balance,
		"total_used":         token.TotalUsed,
		"rate_limit":         token.RateLimit,
		"status":             token.Status,
		"created_at":         token.CreatedAt,
		"channel_priorities": priorityList,
	}, nil
}

func (s *TokenService) UpdateToken(userID uint, id uint, req *UpdateTokenReq) error {
	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ?", id, userID).First(&token).Error; err != nil {
		return err
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		if req.Name != "" {
			if err := tx.Model(&token).Update("name", req.Name).Error; err != nil {
				return err
			}
		}
		if req.ChannelPriorities != nil {
			if err := tx.Where("token_id = ?", id).Delete(&model.TokenChannelPriority{}).Error; err != nil {
				return err
			}
			if len(req.ChannelPriorities) > 0 {
				return saveChannelPriorities(tx, id, req.ChannelPriorities)
			}
		}
		return nil
	})
}

func (s *TokenService) DeleteToken(userID uint, id uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Token{}).Where("id = ? AND user_id = ?", id, userID).Delete(&model.Token{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Where("token_id = ?", id).Delete(&model.TokenChannelPriority{}).Error
	})
}

func (s *TokenService) RechargeToken(userID uint, id uint, amount decimal.Decimal) (*model.Token, error) {
	result := model.DB().Model(&model.Token{}).
		Where("id = ? AND user_id = ?", id, userID).
		UpdateColumn("balance", gorm.Expr("balance + ?", amount))

	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	var token model.Token
	model.DB().First(&token, id)
	return &token, nil
}

func generateAPIKey() string {
	bytes := make([]byte, 24)
	rand.Read(bytes)
	return "sk-prism-" + hex.EncodeToString(bytes)
}

func saveChannelPriorities(tx *gorm.DB, tokenID uint, items []ChannelPriorityInput) error {
	priorities := make([]model.TokenChannelPriority, len(items))
	for i, item := range items {
		priorities[i] = model.TokenChannelPriority{
			TokenID:        tokenID,
			CapabilityCode: item.CapabilityCode,
			ChannelID:      item.ChannelID,
			Priority:       item.Priority,
		}
	}
	return tx.Create(&priorities).Error
}
