package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type TaskRepo struct {
	db *gorm.DB
}

func NewTaskRepo(db *gorm.DB) *TaskRepo {
	return &TaskRepo{db: db}
}

func (r *TaskRepo) FindByID(ctx context.Context, id uint) (*domain.Task, error) {
	var m model.Task
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return taskToDomain(&m), nil
}

func (r *TaskRepo) FindByTaskNo(ctx context.Context, taskNo string) (*domain.Task, error) {
	var m model.Task
	if err := r.db.WithContext(ctx).Where("task_no = ?", taskNo).First(&m).Error; err != nil {
		return nil, wrapErr(err)
	}
	return taskToDomain(&m), nil
}

func (r *TaskRepo) List(ctx context.Context, filter domain.TaskFilter) ([]domain.Task, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Task{})
	if filter.TokenID > 0 {
		q = q.Where("token_id = ?", filter.TokenID)
	}
	if filter.CapabilityCode != "" {
		q = q.Where("model_code = ?", filter.CapabilityCode)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}

	var total int64
	q.Count(&total)

	var list []model.Task
	if filter.Page > 0 && filter.Size > 0 {
		q = q.Offset((filter.Page - 1) * filter.Size).Limit(filter.Size)
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.Task, len(list))
	for i := range list {
		result[i] = *taskToDomain(&list[i])
	}
	return result, total, nil
}

func (r *TaskRepo) Create(ctx context.Context, t *domain.Task) error {
	m := taskToModel(t)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return wrapErr(err)
	}
	t.ID = m.ID
	return nil
}

func (r *TaskRepo) UpdateStatus(ctx context.Context, id uint, status domain.TaskStatus, updates map[string]any) error {
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["status"] = string(status)
	return r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Updates(updates).Error
}

func taskToDomain(m *model.Task) *domain.Task {
	var reqParams, mappedParams, vendorResp, result map[string]any
	if m.RequestParams != nil {
		reqParams = jsonUnmarshalMap([]byte(m.RequestParams))
	}
	if m.MappedParams != nil {
		mappedParams = jsonUnmarshalMap([]byte(m.MappedParams))
	}
	if m.VendorResponse != nil {
		vendorResp = jsonUnmarshalMap([]byte(m.VendorResponse))
	}
	if m.Result != nil {
		result = jsonUnmarshalMap([]byte(m.Result))
	}
	return &domain.Task{
		ID:                  m.ID,
		TaskNo:              m.TaskNo,
		UserID:              m.UserID,
		TokenID:             m.TokenID,
		CapabilityCode:      m.ModelCode,
		ChannelID:           m.ChannelID,
		ChannelCapabilityID: m.EndpointID,
		AccountID:           m.AccountID,
		VendorTaskID:        m.VendorTaskID,
		Status:              domain.TaskStatus(m.Status),
		Progress:            m.Progress,
		CallbackURL:         m.CallbackURL,
		CallbackStatus:      string(m.CallbackStatus),
		CallbackAttempts:    m.CallbackAttempts,
		RequestParams:       reqParams,
		MappedParams:        mappedParams,
		VendorResponse:      vendorResp,
		Result:              result,
		ErrorMessage:        m.ErrorMessage,
		Cost:                m.Cost,
		Refunded:            m.Refunded,
		StartedAt:           m.StartedAt,
		CompletedAt:         m.CompletedAt,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
}

func taskToModel(d *domain.Task) *model.Task {
	m := &model.Task{
		TaskNo:              d.TaskNo,
		UserID:              d.UserID,
		TokenID:             d.TokenID,
		ModelCode:           d.CapabilityCode,
		ChannelID:           d.ChannelID,
		EndpointID:          d.ChannelCapabilityID,
		AccountID:           d.AccountID,
		VendorTaskID:        d.VendorTaskID,
		Status:              model.TaskStatus(d.Status),
		Progress:            d.Progress,
		CallbackURL:         d.CallbackURL,
		CallbackStatus:      model.CallbackStatus(d.CallbackStatus),
		CallbackAttempts:    d.CallbackAttempts,
		ErrorMessage:        d.ErrorMessage,
		Cost:                d.Cost,
		Refunded:            d.Refunded,
		StartedAt:           d.StartedAt,
		CompletedAt:         d.CompletedAt,
	}
	m.ID = d.ID
	if d.RequestParams != nil {
		b, _ := jsonMarshal(d.RequestParams)
		m.RequestParams = b
	}
	if d.MappedParams != nil {
		b, _ := jsonMarshal(d.MappedParams)
		m.MappedParams = b
	}
	if d.VendorResponse != nil {
		b, _ := jsonMarshal(d.VendorResponse)
		m.VendorResponse = b
	}
	if d.Result != nil {
		b, _ := jsonMarshal(d.Result)
		m.Result = b
	}
	return m
}

var _ domain.TaskRepository = (*TaskRepo)(nil)
