package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

// ModelRepo models 表 CRUD
type ModelRepo struct {
	db *gorm.DB
}

func NewModelRepo(db *gorm.DB) *ModelRepo {
	return &ModelRepo{db: db}
}

func (r *ModelRepo) FindByCode(ctx context.Context, code string) (*model.Model, error) {
	var m model.Model
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&m).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &m, nil
}

func (r *ModelRepo) List(ctx context.Context, modelType string, keyword string, page, size int) ([]model.Model, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Model{})
	if modelType != "" {
		q = q.Where("type = ?", modelType)
	}
	if keyword != "" {
		q = q.Where("code LIKE ? OR name LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	q.Count(&total)

	var list []model.Model
	if page > 0 && size > 0 {
		q = q.Offset((page - 1) * size).Limit(size)
	}
	if err := q.Order("code ASC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ModelRepo) Create(ctx context.Context, m *model.Model) error {
	return wrapErr(r.db.WithContext(ctx).Create(m).Error)
}

func (r *ModelRepo) Update(ctx context.Context, m *model.Model) error {
	return wrapErr(r.db.WithContext(ctx).Save(m).Error)
}

func (r *ModelRepo) Delete(ctx context.Context, code string) error {
	return wrapErr(r.db.WithContext(ctx).Where("code = ?", code).Delete(&model.Model{}).Error)
}

// EndpointRepo endpoints 表 CRUD
type EndpointRepo struct {
	db *gorm.DB
}

func NewEndpointRepo(db *gorm.DB) *EndpointRepo {
	return &EndpointRepo{db: db}
}

func (r *EndpointRepo) FindByID(ctx context.Context, id uint) (*model.Endpoint, error) {
	var ep model.Endpoint
	if err := r.db.WithContext(ctx).Preload("Channel").First(&ep, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &ep, nil
}

func (r *EndpointRepo) ListByModel(ctx context.Context, modelCode string) ([]model.Endpoint, error) {
	var list []model.Endpoint
	if err := r.db.WithContext(ctx).Preload("Channel").Where("model_code = ?", modelCode).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *EndpointRepo) ListByChannel(ctx context.Context, channelID uint) ([]model.Endpoint, error) {
	var list []model.Endpoint
	if err := r.db.WithContext(ctx).Where("channel_id = ?", channelID).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *EndpointRepo) Create(ctx context.Context, ep *model.Endpoint) error {
	return wrapErr(r.db.WithContext(ctx).Create(ep).Error)
}

func (r *EndpointRepo) Update(ctx context.Context, ep *model.Endpoint) error {
	return wrapErr(r.db.WithContext(ctx).Save(ep).Error)
}

func (r *EndpointRepo) Delete(ctx context.Context, id uint) error {
	return wrapErr(r.db.WithContext(ctx).Delete(&model.Endpoint{}, id).Error)
}
