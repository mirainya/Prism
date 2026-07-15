package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type UserService struct{}

func NewUserService() *UserService {
	return &UserService{}
}

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token string      `json:"token"`
	User  *model.User `json:"user"`
}

func (s *UserService) Register(req *RegisterRequest) (*model.User, error) {
	var exist int64
	err := model.DB().Model(&model.User{}).Where("username = ?", req.Username).Count(&exist).Error
	if err != nil || exist > 0 {
		return nil, errors.New("username already exists")
	}

	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Username: req.Username,
		Password: hashedPassword,
		Role:     model.UserRoleUser,
		Status:   1,
	}

	if err := model.DB().Model(&model.User{}).Create(user).Error; err != nil {
		return nil, err
	}

	return user, nil
}

func (s *UserService) Login(req *LoginRequest) (*LoginResponse, error) {
	var user model.User
	if err := model.DB().Model(&model.User{}).Where("username = ? AND status = 1", req.Username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid username or password")
		}
		return nil, err
	}

	if !auth.CheckPassword(req.Password, user.Password) {
		return nil, errors.New("invalid username or password")
	}

	token, err := auth.GenerateTokenWithSessionVersion(user.ID, user.Username, string(user.Role), user.SessionVersion)
	if err != nil {
		return nil, err
	}

	// 将登录 token 存入缓存
	if err := cache.SetLoginToken(context.Background(), token, user.ID); err != nil {
		logger.Error("failed to cache login token: " + err.Error())
		return nil, errors.New("login failed, please try again")
	}

	return &LoginResponse{
		Token: token,
		User:  &user,
	}, nil
}

// Logout 用户登出，删除缓存中的 token
func (s *UserService) Logout(token string) error {
	return cache.DeleteLoginToken(context.Background(), token)
}

func (s *UserService) GetUserByID(id uint) (*model.User, error) {
	var user model.User
	if err := model.DB().Model(&model.User{}).First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) ListUsers() ([]model.User, error) {
	var users []model.User
	if err := model.DB().Model(&model.User{}).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (s *UserService) UpdateUserRole(userID uint, role model.UserRole) error {
	if err := model.DB().Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"role":            role,
		"session_version": gorm.Expr("session_version + 1"),
	}).Error; err != nil {
		return err
	}
	revokeUserSessions(userID)
	return nil
}

func (s *UserService) UpdateUserStatus(userID uint, status int8) error {
	if err := model.DB().Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"status":          status,
		"session_version": gorm.Expr("session_version + 1"),
	}).Error; err != nil {
		return err
	}
	revokeUserSessions(userID)
	return nil
}

// RechargeUser 给指定用户充值额度
func (s *UserService) RechargeUser(userID uint, amount decimal.Decimal) error {
	return s.RechargeUserBy(0, userID, amount)
}

func (s *UserService) RechargeUserBy(actorUserID, userID uint, amount decimal.Decimal) error {
	if !amount.IsPositive() {
		return ErrInvalidBalanceAmount
	}
	return model.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).Where("id = ?", userID).
			UpdateColumn("balance", gorm.Expr("balance + ?", amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return recordBalanceEntryTx(tx, balanceEntryRequest{
			AccountType: model.BalanceAccountUser, AccountID: userID,
			UserID: userID, Direction: model.BalanceDirectionCredit,
			Category: BalanceCategoryRecharge, Amount: amount,
			SourceKey: "user_recharge:" + uuid.NewString(), ActorUserID: actorUserID,
		})
	})
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// ChangePassword 修改用户密码
func (s *UserService) ChangePassword(userID uint, req *ChangePasswordRequest) error {
	var user model.User
	if err := model.DB().Model(&model.User{}).First(&user, userID).Error; err != nil {
		return errors.New("user not found")
	}

	if !auth.CheckPassword(req.OldPassword, user.Password) {
		return errors.New("incorrect old password")
	}

	hashedPassword, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}

	if err := model.DB().Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"password":        hashedPassword,
		"session_version": gorm.Expr("session_version + 1"),
	}).Error; err != nil {
		return err
	}
	revokeUserSessions(userID)
	return nil
}

func revokeUserSessions(userID uint) {
	if cache.Client == nil {
		return
	}
	if err := cache.DeleteUserLoginTokens(context.Background(), userID); err != nil {
		logger.Error("failed to revoke user sessions: " + err.Error())
	}
}
