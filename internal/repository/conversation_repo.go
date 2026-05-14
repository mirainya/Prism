package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type ConversationRepo struct {
	db *gorm.DB
}

func NewConversationRepo(db *gorm.DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

func (r *ConversationRepo) FindByID(ctx context.Context, id uint) (*domain.Conversation, error) {
	var m model.Conversation
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return convToDomain(&m), nil
}

func (r *ConversationRepo) ListByUser(ctx context.Context, userID uint, limit, offset int) ([]domain.Conversation, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Conversation{}).Where("user_id = ?", userID)

	var total int64
	q.Count(&total)

	var list []model.Conversation
	if err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.Conversation, len(list))
	for i := range list {
		result[i] = *convToDomain(&list[i])
	}
	return result, total, nil
}

func (r *ConversationRepo) Create(ctx context.Context, conv *domain.Conversation) error {
	m := &model.Conversation{
		UserID:       conv.UserID,
		TokenID:      conv.TokenID,
		Title:        conv.Title,
		Model:        conv.Model,
		SystemPrompt: conv.SystemPrompt,
		Status:       conv.Status,
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return err
	}
	conv.ID = m.ID
	return nil
}

func (r *ConversationRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("conversation_id = ?", id).Delete(&model.Message{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Conversation{}, id).Error
	})
}

func (r *ConversationRepo) AddMessage(ctx context.Context, msg *domain.Message) error {
	m := &model.Message{
		ConversationID:   msg.ConversationID,
		RequestLogID:     msg.RequestLogID,
		Role:             msg.Role,
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
		FinishReason:     msg.FinishReason,
		InputTokens:      msg.InputTokens,
		OutputTokens:     msg.OutputTokens,
		Model:            msg.Model,
		ChannelID:        msg.ChannelID,
		AccountID:        msg.AccountID,
		LatencyMs:        msg.LatencyMs,
		Cost:             msg.Cost,
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return err
	}
	msg.ID = m.ID
	return nil
}

func (r *ConversationRepo) ListMessages(ctx context.Context, conversationID uint) ([]domain.Message, error) {
	var list []model.Message
	if err := r.db.WithContext(ctx).Where("conversation_id = ?", conversationID).
		Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}

	result := make([]domain.Message, len(list))
	for i, m := range list {
		result[i] = domain.Message{
			ID:               m.ID,
			ConversationID:   m.ConversationID,
			RequestLogID:     m.RequestLogID,
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			FinishReason:     m.FinishReason,
			InputTokens:      m.InputTokens,
			OutputTokens:     m.OutputTokens,
			Model:            m.Model,
			ChannelID:        m.ChannelID,
			AccountID:        m.AccountID,
			LatencyMs:        m.LatencyMs,
			Cost:             m.Cost,
			CreatedAt:        m.CreatedAt,
		}
	}
	return result, nil
}

func convToDomain(m *model.Conversation) *domain.Conversation {
	return &domain.Conversation{
		ID:               m.ID,
		UserID:           m.UserID,
		TokenID:          m.TokenID,
		Title:            m.Title,
		Model:            m.Model,
		SystemPrompt:     m.SystemPrompt,
		LastRequestLogID: m.LastRequestLogID,
		LastStatus:       m.LastStatus,
		TotalTokens:      m.TotalTokens,
		MessageCount:     m.MessageCount,
		Status:           m.Status,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

var _ domain.ConversationRepository = (*ConversationRepo)(nil)
