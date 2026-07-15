package service

import (
	"github.com/mirainya/Prism/internal/model"
)

type ConversationTurnItem struct {
	model.ConversationTurn
	Items []model.ConversationItem `json:"items"`
}

type ListConversationTurnsResponse struct {
	Items    []ConversationTurnItem `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

func (s *ConversationService) ListTurns(conversationID uint, page, pageSize int) (*ListConversationTurnsResponse, error) {
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
	var total int64
	if err := db.Model(&model.ConversationTurn{}).
		Where("conversation_id = ?", conversationID).Count(&total).Error; err != nil {
		return nil, err
	}
	var turns []model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).
		Order("turn_sequence ASC").Order("id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&turns).Error; err != nil {
		return nil, err
	}
	result := &ListConversationTurnsResponse{
		Items: make([]ConversationTurnItem, len(turns)), Total: total, Page: page, PageSize: pageSize,
	}
	if len(turns) == 0 {
		return result, nil
	}
	turnIDs := make([]uint64, len(turns))
	for index := range turns {
		turnIDs[index] = turns[index].ID
		result.Items[index] = ConversationTurnItem{ConversationTurn: turns[index], Items: make([]model.ConversationItem, 0)}
	}
	var items []model.ConversationItem
	if err := db.Where("conversation_id = ? AND turn_id IN ?", conversationID, turnIDs).
		Order("turn_sequence ASC").Order("ordinal ASC").Order("id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	indexByTurnID := make(map[uint64]int, len(turns))
	for index, turn := range turns {
		indexByTurnID[turn.ID] = index
	}
	for _, item := range items {
		if index, ok := indexByTurnID[item.TurnID]; ok {
			result.Items[index].Items = append(result.Items[index].Items, item)
		}
	}
	return result, nil
}
