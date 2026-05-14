package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type UserRepo struct {
	db *gorm.DB
}

func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) FindByID(ctx context.Context, id uint) (*domain.User, error) {
	var m model.User
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return userToDomain(&m), nil
}

func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	var m model.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&m).Error; err != nil {
		return nil, wrapErr(err)
	}
	return userToDomain(&m), nil
}

func (r *UserRepo) List(ctx context.Context, filter domain.UserFilter) ([]domain.User, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.User{})
	if filter.Role != "" {
		q = q.Where("role = ?", filter.Role)
	}
	if filter.Keyword != "" {
		q = q.Where("username LIKE ?", "%"+filter.Keyword+"%")
	}

	var total int64
	q.Count(&total)

	var list []model.User
	if filter.Page > 0 && filter.Size > 0 {
		q = q.Offset((filter.Page - 1) * filter.Size).Limit(filter.Size)
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.User, len(list))
	for i := range list {
		result[i] = *userToDomain(&list[i])
	}
	return result, total, nil
}

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	m := userToModel(u)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return wrapErr(err)
	}
	u.ID = m.ID
	return nil
}

func (r *UserRepo) Update(ctx context.Context, u *domain.User) error {
	m := userToModel(u)
	return wrapErr(r.db.WithContext(ctx).Save(m).Error)
}

func (r *UserRepo) Delete(ctx context.Context, id uint) error {
	return wrapErr(r.db.WithContext(ctx).Delete(&model.User{}, id).Error)
}

func userToDomain(m *model.User) *domain.User {
	return &domain.User{
		ID:        m.ID,
		Username:  m.Username,
		Password:  m.Password,
		Role:      string(m.Role),
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func userToModel(d *domain.User) *model.User {
	m := &model.User{
		Username: d.Username,
		Password: d.Password,
		Role:     model.UserRole(d.Role),
		Status:   d.Status,
	}
	m.ID = d.ID
	return m
}
