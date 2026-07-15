package service

import (
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type ConversationService struct{}

func NewConversationService() *ConversationService {
	return &ConversationService{}
}

// ListConversationsRequest 查询对话列表参数
type ListConversationsRequest struct {
	Page      int    `form:"page"`
	PageSize  int    `form:"page_size"`
	UserID    uint   `form:"user_id"`
	Model     string `form:"model"`
	Keyword   string `form:"keyword"`
	TokenID   uint   `form:"token_id"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// ConversationItem 对话列表项
type ConversationItem struct {
	model.Conversation
	TotalCost decimal.Decimal `json:"total_cost"`
}

// ListConversationsResponse 查询对话列表响应
type ListConversationsResponse struct {
	Items    []ConversationItem `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

// ListConversations 查询对话列表
func (s *ConversationService) ListConversations(req *ListConversationsRequest) (*ListConversationsResponse, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}

	db := model.DB()

	applyFilters := func(q *gorm.DB) *gorm.DB {
		if req.UserID > 0 {
			q = q.Where("user_id = ?", req.UserID)
		}
		if req.Model != "" {
			q = q.Where("model = ?", req.Model)
		}
		if req.Keyword != "" {
			q = q.Where("title LIKE ?", "%"+req.Keyword+"%")
		}
		if req.TokenID > 0 {
			q = q.Where("token_id = ?", req.TokenID)
		}
		if req.StartDate != "" {
			q = q.Where("created_at >= ?", req.StartDate+" 00:00:00")
		}
		if req.EndDate != "" {
			q = q.Where("created_at <= ?", req.EndDate+" 23:59:59")
		}
		return q
	}

	var total int64
	if err := applyFilters(db.Model(&model.Conversation{})).Count(&total).Error; err != nil {
		return nil, err
	}

	// 先分页查出当前页对话
	var conversations []model.Conversation
	offset := (req.Page - 1) * req.PageSize
	if err := applyFilters(db.Model(&model.Conversation{})).
		Order("id DESC").Offset(offset).Limit(req.PageSize).
		Find(&conversations).Error; err != nil {
		return nil, err
	}

	items := make([]ConversationItem, len(conversations))
	if len(conversations) > 0 {
		ids := make([]uint, len(conversations))
		for i, c := range conversations {
			ids[i] = c.ID
			items[i] = ConversationItem{Conversation: c}
		}

		// New conversations use the turn ledger. Legacy rows without turns fall
		// back to messages so existing API responses remain compatible.
		costMap, err := aggregateConversationCosts(db, "conversation_turns", ids)
		if err != nil {
			return nil, err
		}
		legacyIDs := make([]uint, 0, len(ids))
		for _, id := range ids {
			if _, ok := costMap[id]; !ok {
				legacyIDs = append(legacyIDs, id)
			}
		}
		if len(legacyIDs) > 0 {
			legacyCosts, err := aggregateConversationCosts(db, "messages", legacyIDs)
			if err != nil {
				return nil, err
			}
			for conversationID, cost := range legacyCosts {
				costMap[conversationID] = cost
			}
		}
		for i := range items {
			items[i].TotalCost = costMap[items[i].ID]
		}
	}

	return &ListConversationsResponse{
		Items:    items,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

func aggregateConversationCosts(db *gorm.DB, table string, conversationIDs []uint) (map[uint]decimal.Decimal, error) {
	type costRow struct {
		ConversationID uint            `gorm:"column:conversation_id"`
		Cost           decimal.Decimal `gorm:"column:cost"`
		TotalCost      decimal.Decimal `gorm:"column:total_cost"`
	}
	result := make(map[uint]decimal.Decimal, len(conversationIDs))
	query := db.Table(table).Where("conversation_id IN ?", conversationIDs)
	if db.Dialector.Name() == "sqlite" {
		var rows []costRow
		if err := query.Select("conversation_id, cost").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result[row.ConversationID] = result[row.ConversationID].Add(row.Cost)
		}
		return result, nil
	}
	var rows []costRow
	if err := query.Select("conversation_id, SUM(cost) AS total_cost").Group("conversation_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ConversationID] = row.TotalCost
	}
	return result, nil
}

// GetConversation 获取单个对话
func (s *ConversationService) GetConversation(id uint) (*model.Conversation, error) {
	var conversation model.Conversation
	if err := model.DB().First(&conversation, id).Error; err != nil {
		return nil, err
	}
	return &conversation, nil
}

// ListMessagesRequest 查询消息列表参数
type ListMessagesRequest struct {
	ConversationID uint `form:"conversation_id"`
	Page           int  `form:"page"`
	PageSize       int  `form:"page_size"`
}

// ListMessagesResponse 查询消息列表响应
type ListMessagesResponse struct {
	Items        []model.Message     `json:"items"`
	Total        int64               `json:"total"`
	Page         int                 `json:"page"`
	PageSize     int                 `json:"page_size"`
	Conversation *model.Conversation `json:"conversation"`
}

// ListMessages 查询消息列表
func (s *ConversationService) ListMessages(conversationID uint, page, pageSize int) (*ListMessagesResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	db := model.DB()

	// 获取对话信息
	var conversation model.Conversation
	if err := db.First(&conversation, conversationID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, err
		}
		return nil, err
	}

	// 总数
	var total int64
	if err := db.Model(&model.Message{}).Where("conversation_id = ?", conversationID).Count(&total).Error; err != nil {
		return nil, err
	}

	// 分页查询，按创建时间正序
	var items []model.Message
	offset := (page - 1) * pageSize
	if err := db.Where("conversation_id = ?", conversationID).
		Order("created_at ASC, id ASC").
		Offset(offset).Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, err
	}

	return &ListMessagesResponse{
		Items:        items,
		Total:        total,
		Page:         page,
		PageSize:     pageSize,
		Conversation: &conversation,
	}, nil
}
