package service

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
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

		costMap, err := aggregateConversationCosts(db, "conversation_turns", ids)
		if err != nil {
			return nil, err
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
	Items        []ConversationMessage `json:"items"`
	Total        int64                 `json:"total"`
	Page         int                   `json:"page"`
	PageSize     int                   `json:"page_size"`
	Conversation *model.Conversation   `json:"conversation"`
}

// ConversationMessage is a readable projection of canonical conversation
// items. The canonical item rows remain the only stored message history.
type ConversationMessage struct {
	ID               uint64
	ConversationID   uint
	CallID           string
	RequestLogID     uint
	Role             string
	Content          string
	Attachments      string
	ReasoningContent string
	FinishReason     string
	InputTokens      int
	OutputTokens     int
	Model            string
	ChannelID        uint
	AccountID        uint
	LatencyMs        int
	Cost             decimal.Decimal
	CreatedAt        time.Time
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

	items, err := projectConversationMessages(db, conversationID)
	if err != nil {
		return nil, err
	}
	total := int64(len(items))
	offset := (page - 1) * pageSize
	if offset >= len(items) {
		items = make([]ConversationMessage, 0)
	} else {
		end := offset + pageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[offset:end]
	}

	return &ListMessagesResponse{
		Items:        items,
		Total:        total,
		Page:         page,
		PageSize:     pageSize,
		Conversation: &conversation,
	}, nil
}

func projectConversationMessages(db *gorm.DB, conversationID uint) ([]ConversationMessage, error) {
	var turns []model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).
		Order("turn_sequence ASC").Order("id ASC").Find(&turns).Error; err != nil {
		return nil, err
	}
	if len(turns) == 0 {
		return make([]ConversationMessage, 0), nil
	}
	turnIDs := make([]uint64, len(turns))
	for index := range turns {
		turnIDs[index] = turns[index].ID
	}
	var records []model.ConversationItem
	if err := db.Where("conversation_id = ? AND turn_id IN ?", conversationID, turnIDs).
		Order("turn_sequence ASC").Order("ordinal ASC").Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	recordsByTurn := make(map[uint64][]model.ConversationItem, len(turns))
	for _, record := range records {
		recordsByTurn[record.TurnID] = append(recordsByTurn[record.TurnID], record)
	}
	result := make([]ConversationMessage, 0, len(records))
	for _, turn := range turns {
		turnRecords := recordsByTurn[turn.ID]
		for _, direction := range []string{model.ConversationItemInput, model.ConversationItemOutput} {
			directional := make([]model.ConversationItem, 0, len(turnRecords))
			canonicalItems := make([]canonical.Item, 0, len(turnRecords))
			for _, record := range turnRecords {
				if record.Direction != direction {
					continue
				}
				var item canonical.Item
				if err := json.Unmarshal(record.CanonicalJSON, &item); err != nil {
					return nil, fmt.Errorf("decode conversation item %d: %w", record.ID, err)
				}
				directional = append(directional, record)
				canonicalItems = append(canonicalItems, item)
			}
			messages, err := canonicalItemsToChatMessages(canonicalItems)
			if err != nil {
				return nil, fmt.Errorf("project conversation turn %d: %w", turn.ID, err)
			}
			for index, message := range messages {
				id := turn.ID
				if len(directional) > 0 {
					recordIndex := index
					if recordIndex >= len(directional) {
						recordIndex = len(directional) - 1
					}
					id = directional[recordIndex].ID
				}
				projected := ConversationMessage{
					ID: id, ConversationID: conversationID, CallID: turn.CallID,
					RequestLogID: turn.RequestLogID, Role: message.Role,
					Content: message.ContentText(), Attachments: message.ContentAttachments(),
					ReasoningContent: message.ReasoningContent, Model: turn.Model, CreatedAt: turn.CreatedAt,
				}
				if direction == model.ConversationItemOutput && index == len(messages)-1 {
					projected.FinishReason = turn.FinishReason
					projected.InputTokens = turn.InputTokens
					projected.OutputTokens = turn.OutputTokens
					projected.LatencyMs = int(turn.LatencyMs)
					projected.Cost = turn.Cost
				}
				result = append(result, projected)
			}
		}
	}
	return result, nil
}
