package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TokenService struct {
	probeFileStorage func(context.Context, string) error
}

func NewTokenService() *TokenService {
	return &TokenService{probeFileStorage: probeTokenFileStorage}
}

type CreateTokenReq struct {
	Name    string          `json:"name" binding:"required,max=50"`
	Balance decimal.Decimal `json:"balance"`
}

type UpdateTokenReq struct {
	Name string `json:"name" binding:"max=50"`
}

func (s *TokenService) ListTokens(userID uint) ([]gin.H, error) {
	var tokens []model.Token
	if err := model.DB().Model(&model.Token{}).
		Where("user_id = ? AND status = 1 AND revoked_at IS NULL", userID).
		Find(&tokens).Error; err != nil {
		return nil, err
	}

	tokenIDs := make([]uint, len(tokens))
	for i, t := range tokens {
		tokenIDs[i] = t.ID
	}
	funds, err := loadTokenFunds(context.Background(), tokenIDs)
	if err != nil {
		return nil, err
	}

	result := make([]gin.H, len(tokens))
	for i, t := range tokens {
		keyHint := tokenKeyHint(&t)
		snapshot := funds[t.ID]
		result[i] = gin.H{
			"id":          t.ID,
			"name":        t.Name,
			"key":         keyHint,
			"key_hint":    keyHint,
			"balance":     snapshot.Available,
			"total_used":  snapshot.Used,
			"rate_limit":  t.RateLimit,
			"status":      t.Status,
			"created_at":  t.CreatedAt,
			"xfs_storage": newTokenFileStorageStatus(t.XFSAPIKey),
		}
	}

	return result, nil
}

func (s *TokenService) CreateToken(userID uint, req *CreateTokenReq) (gin.H, error) {
	if req.Balance.IsNegative() {
		return nil, ErrInvalidBalanceAmount
	}
	plainKey, selector, secretDigest, err := tokenauth.Generate()
	if err != nil {
		return nil, err
	}
	keyHint := tokenauth.KeyHint(plainKey)

	store, err := unifiedFundsStore()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	var tokenID uint64
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := store.OpenBillingAccount(ctx, tx, uint64(userID)); err != nil {
			return err
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO tokens(user_id,selector,secret_digest,secret_digest_version,auth_version,key_hint,name,rate_limit,status,created_at,updated_at) VALUES (?,?,?,?,1,?,?,60,1,?,?)`,
			userID, selector, secretDigest, tokenauth.DigestVersion, keyHint, req.Name, createdAt, createdAt)
		if err != nil {
			return err
		}
		id, err := inserted.LastInsertId()
		if err != nil || id <= 0 {
			return repository.ErrConflict
		}
		tokenID = uint64(id)
		limit := req.Balance.String()
		policyID, err := store.CreateBudgetPolicy(ctx, tx, repository.BudgetPolicyInput{
			TokenID: tokenID, PolicyCode: "default-lifetime", WindowKind: "lifetime",
			TimezoneName: "UTC", LimitAmount: &limit, AlgorithmVersion: 1,
		})
		if err != nil {
			return err
		}
		activationID, err := store.ActivateBudgetPolicy(ctx, tx, tokenID, policyID, createdAt)
		if err != nil {
			return err
		}
		_, err = store.CreateBudgetWindow(ctx, tx, repository.BudgetWindowInput{
			TokenID: tokenID, PolicyID: policyID, ActivationID: activationID, StartAt: createdAt,
		})
		return err
	})

	if err != nil {
		return nil, err
	}

	return gin.H{
		"id":      uint(tokenID),
		"name":    req.Name,
		"key":     plainKey,
		"balance": req.Balance,
	}, nil
}

func (s *TokenService) GetToken(userID uint, id uint) (gin.H, error) {
	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", id, userID).First(&token).Error; err != nil {
		return nil, err
	}

	funds, err := loadTokenFunds(context.Background(), []uint{id})
	if err != nil {
		return nil, err
	}
	keyHint := tokenKeyHint(&token)
	snapshot := funds[id]
	return gin.H{
		"id":          token.ID,
		"name":        token.Name,
		"key":         keyHint,
		"key_hint":    keyHint,
		"balance":     snapshot.Available,
		"total_used":  snapshot.Used,
		"rate_limit":  token.RateLimit,
		"status":      token.Status,
		"created_at":  token.CreatedAt,
		"xfs_storage": newTokenFileStorageStatus(token.XFSAPIKey),
	}, nil
}

func (s *TokenService) UpdateToken(userID uint, id uint, req *UpdateTokenReq) error {
	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ? AND status = 1 AND revoked_at IS NULL", id, userID).First(&token).Error; err != nil {
		return err
	}

	if req.Name == "" {
		return nil
	}
	return model.DB().Model(&token).Update("name", req.Name).Error
}

func (s *TokenService) DeleteToken(userID uint, id uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		var token model.Token
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id=? AND user_id=?", id, userID).First(&token).Error; err != nil {
			return err
		}
		if token.Status == 0 && token.RevokedAt != nil {
			return nil
		}
		if token.AuthVersion == math.MaxUint64 {
			return repository.ErrConflict
		}
		now := time.Now().UTC()
		newVersion := token.AuthVersion + 1
		result := tx.Model(&model.Token{}).Where("id=? AND auth_version=?", id, token.AuthVersion).
			Updates(map[string]any{"status": 0, "revoked_at": now, "auth_version": newVersion})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return repository.ErrConflict
		}
		return tx.Exec(`INSERT INTO token_auth_state_events(token_id,auth_version,old_status,new_status,reason_code,actor_user_id,created_at) VALUES (?,?,?,?,?,?,?)`,
			id, newVersion, token.Status, 0, "owner_revoked", userID, now).Error
	})
}

type TokenFunds struct {
	ID        uint
	Balance   decimal.Decimal
	TotalUsed decimal.Decimal
}

func (s *TokenService) RechargeToken(userID uint, id uint, amount decimal.Decimal) (*TokenFunds, error) {
	if !amount.IsPositive() {
		return nil, ErrInvalidBalanceAmount
	}
	store, err := unifiedFundsStore()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	var snapshot fundsSnapshot
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		windowID, err := currentTokenBudgetWindow(ctx, tx, uint64(userID), uint64(id))
		if err != nil {
			return err
		}
		_, _, err = store.ApplyBudgetAdjustment(ctx, tx, repository.BudgetAdjustmentInput{
			WindowID: windowID, AmountDelta: amount.String(), SourceType: "recharge",
			SourceKey: "token_recharge:" + uuid.NewString(),
		})
		if err != nil {
			return err
		}
		snapshot, err = readBudgetWindowFunds(ctx, tx, windowID)
		return err
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	return &TokenFunds{ID: id, Balance: snapshot.Available, TotalUsed: snapshot.Used}, nil
}

func tokenKeyHint(token *model.Token) string {
	if len(token.KeyHint) >= 4 && token.KeyHint[:4] == "****" {
		return token.KeyHint
	}
	return "****"
}
