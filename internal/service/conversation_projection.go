package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	canonicalConversationMatchLimit   = 100
	canonicalConversationScanPageSize = 128
)

var ErrConversationProjectionDependencyPending = errors.New("previous response conversation projection is pending")

// 本文件把不同下游协议的 canonical 输入、输出投影为统一会话历史。
// 续话请求通常会再次携带完整历史，核心难点是识别并去掉已保存前缀，同时保留本轮新增输入。

// ConversationProjectionRequest is the protocol-neutral contract used by
// public API handlers to project one gateway call into readable conversation
// history. InputItems normally contains the downstream request history. A
// durable outbox may instead supply a precomputed explicit-conversation delta.
type ConversationProjectionRequest struct {
	UserID             uint
	TokenID            uint
	Model              string
	CallID             string
	RequestLogID       uint
	ProviderResponseID string

	// ConversationID is Prism's explicit conversation identifier. When it is
	// absent, PreviousResponseID is resolved before canonical prefix matching.
	ConversationID     uint
	PreviousResponseID string

	InputItems    []canonical.Item
	OutputItems   []canonical.Item
	InputPrepared bool
	ContextMode   model.ConversationTurnContextMode

	Status       model.ConversationTurnStatus
	FinishReason string
	ErrorType    string
	ErrorCode    string
	ErrorMessage string
	Provenance   ConversationProvenance
}

// ProjectAPIConversation records one public API call as a conversation turn.
// Repeating the same CallID returns the original conversation without writing
// duplicate turns or items.
func ProjectAPIConversation(request ConversationProjectionRequest) (uint, error) {
	if request.Status == "" {
		status, err := projectedConversationTurnStatus(request.CallID)
		if err != nil {
			return 0, err
		}
		request.Status = status
	}
	return RecordConversationTurn(nil, ConversationTurnRecord{
		UserID: request.UserID, TokenID: request.TokenID, Model: request.Model,
		InputItems:     canonical.CloneItems(request.InputItems),
		OutputItems:    primaryConversationOutputItems(request.OutputItems),
		ConversationID: request.ConversationID, PreviousResponseID: strings.TrimSpace(request.PreviousResponseID),
		InputPrepared: request.InputPrepared, ContextMode: request.ContextMode,
		MatchCanonicalInput: true, Status: request.Status, FinishReason: request.FinishReason,
		ProviderResponseID: request.ProviderResponseID, CallID: request.CallID,
		RequestLogID: request.RequestLogID, ErrorType: request.ErrorType,
		ErrorCode: request.ErrorCode, ErrorMessage: request.ErrorMessage,
		Provenance: request.Provenance,
	})
}

func primaryConversationOutputItems(items []canonical.Item) []canonical.Item {
	primary := make([]canonical.Item, 0, len(items))
	for _, item := range items {
		if canonicalConversationChoiceIndex(item) > 0 {
			continue
		}
		primary = append(primary, canonical.CloneItems([]canonical.Item{item})[0])
	}
	return primary
}

func canonicalConversationChoiceIndex(item canonical.Item) int {
	for _, key := range []string{"openai_chat.choice_index", "prism.choice_index"} {
		raw := item.Extra[key]
		if len(raw) == 0 {
			continue
		}
		var number int
		if json.Unmarshal(raw, &number) == nil {
			return number
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			if parsed, err := strconv.Atoi(text); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func projectedConversationTurnStatus(callID string) (model.ConversationTurnStatus, error) {
	var call model.APICall
	if err := model.DB().Select("status").First(&call, "id = ?", callID).Error; err != nil {
		return "", fmt.Errorf("load API call %s status: %w", callID, err)
	}
	return conversationTurnStatusForAPICall(&call)
}

func conversationTurnStatusForAPICall(call *model.APICall) (model.ConversationTurnStatus, error) {
	if call == nil {
		return "", errors.New("API call is required")
	}
	switch call.Status {
	case model.APICallStatusCompleted:
		return model.ConversationTurnCompleted, nil
	case model.APICallStatusFailed:
		return model.ConversationTurnFailed, nil
	case model.APICallStatusCancelled:
		return model.ConversationTurnAborted, nil
	default:
		return "", fmt.Errorf("API call %s is not terminal: %s", call.ID, call.Status)
	}
}

// ValidateAPIConversationID performs the ownership check handlers need before
// spending upstream resources. Projection repeats the check transactionally.
func ValidateAPIConversationID(conversationID, userID, tokenID uint) error {
	if conversationID == 0 {
		return nil
	}
	var count int64
	if err := model.DB().Model(&model.Conversation{}).
		Where("id = ? AND user_id = ? AND token_id = ? AND status = 1", conversationID, userID, tokenID).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrConversationNotFound
	}
	return nil
}

func recordUsesCanonicalItems(record *ConversationTurnRecord) bool {
	return record != nil && (record.MatchCanonicalInput || record.InputItems != nil || record.OutputItems != nil)
}

func hydrateConversationTurnRecordTx(tx *gorm.DB, record *ConversationTurnRecord, call *model.APICall) error {
	if call.Model != "" {
		record.Model = call.Model
	}
	attemptID := call.FinalAttemptID
	if attemptID > 0 {
		var attempt model.APICallAttempt
		if err := tx.Where("id = ? AND call_id = ?", attemptID, call.ID).First(&attempt).Error; err != nil {
			return fmt.Errorf("load final API call attempt %d: %w", attemptID, err)
		}
		if attempt.ProviderResponseID != "" {
			record.ProviderResponseID = attempt.ProviderResponseID
		}
		record.Provenance.KeyID = attempt.KeyID
		record.Provenance.Transport = attempt.Transport
	}
	var requestLog model.ChannelRequestLog
	if record.RequestLogID > 0 {
		query := tx.Where("id = ? AND call_id = ?", record.RequestLogID, call.ID)
		if attemptID > 0 {
			query = query.Where("attempt_id = ?", attemptID)
		}
		if err := query.First(&requestLog).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load conversation request log %d for call %s: %w", record.RequestLogID, call.ID, err)
			}
			var existing int64
			if countErr := tx.Model(&model.ChannelRequestLog{}).
				Where("id = ?", record.RequestLogID).Count(&existing).Error; countErr != nil {
				return countErr
			}
			if existing > 0 {
				return fmt.Errorf("load conversation request log %d for call %s: %w", record.RequestLogID, call.ID, err)
			}
			record.RequestLogID = 0
		}
	}
	if requestLog.ID == 0 {
		query := tx.Where("call_id = ?", call.ID)
		if attemptID > 0 {
			query = query.Where("attempt_id = ?", attemptID)
		}
		err := query.Order("id DESC").First(&requestLog).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			record.RequestLogID = requestLog.ID
		}
	}
	if requestLog.ID > 0 && record.FinishReason == "" {
		record.FinishReason = requestLog.FinishReason
	}
	return nil
}

func resolveCanonicalConversationForTurnTx(tx *gorm.DB, record *ConversationTurnRecord, call *model.APICall) (*model.Conversation, []canonical.Item, model.ConversationTurnContextMode, error) {
	// 解析优先级固定为：已准备输入 -> 显式会话 -> Call 绑定会话 -> previous_response_id -> 历史前缀匹配。
	// 越明确的关联越先使用，避免内容相同的多个会话被自动匹配到错误目标。
	if record.InputPrepared {
		conversation, err := loadOwnedConversationForUpdateTx(tx, record.ConversationID, record.UserID, record.TokenID)
		if err != nil {
			return nil, nil, "", err
		}
		mode := record.ContextMode
		if mode == "" || mode == model.ConversationTurnContextLegacy {
			mode = explicitConversationContextMode(false, record.InputItems)
		}
		return conversation, canonical.CloneItems(record.InputItems), mode, nil
	}
	if record.ConversationID > 0 {
		conversation, err := loadOwnedConversationForUpdateTx(tx, record.ConversationID, record.UserID, record.TokenID)
		if err != nil {
			return nil, nil, "", err
		}
		input, matched, err := trimCanonicalConversationPrefixTx(tx, conversation, record.InputItems)
		return conversation, input, explicitConversationContextMode(matched, input), err
	}
	if call != nil && call.ConversationID > 0 {
		conversation, err := loadOwnedConversationForUpdateTx(tx, call.ConversationID, record.UserID, record.TokenID)
		if err != nil {
			return nil, nil, "", err
		}
		input, matched, err := trimCanonicalConversationPrefixTx(tx, conversation, record.InputItems)
		return conversation, input, explicitConversationContextMode(matched, input), err
	}
	if record.PreviousResponseID != "" {
		conversation, found, err := resolvePreviousResponseConversationTx(tx, record.PreviousResponseID, record.UserID, record.TokenID)
		if err != nil {
			return nil, nil, "", err
		}
		if found {
			input, matched, err := trimCanonicalConversationPrefixTx(tx, conversation, record.InputItems)
			return conversation, input, explicitConversationContextMode(matched, input), err
		}
	}
	if record.MatchCanonicalInput {
		conversation, prefixLength, found, err := matchCanonicalConversationPrefixTx(tx, record.UserID, record.TokenID, record.InputItems)
		if err != nil {
			return nil, nil, "", err
		}
		if found {
			locked, err := loadOwnedConversationForUpdateTx(tx, conversation.ID, record.UserID, record.TokenID)
			if err != nil {
				return nil, nil, "", err
			}
			consumed, matchedLength, matches, err := canonicalConversationPrefixForLockedConversationTx(tx, locked, record.InputItems)
			if err != nil {
				return nil, nil, "", err
			}
			if matches && matchedLength >= prefixLength {
				return locked, canonical.CloneItems(record.InputItems[consumed:]), model.ConversationTurnContextInferred, nil
			}
		}
	}
	conversation, err := createCanonicalConversationTx(tx, record.UserID, record.TokenID, record.Model, record.InputItems)
	if err != nil {
		return nil, nil, "", err
	}
	mode := model.ConversationTurnContextNew
	if canonicalConversationInputLooksLikeSnapshot(record.InputItems) {
		mode = model.ConversationTurnContextSnapshot
	}
	return conversation, canonical.CloneItems(record.InputItems), mode, nil
}

func explicitConversationContextMode(matched bool, input []canonical.Item) model.ConversationTurnContextMode {
	if !matched && canonicalConversationInputLooksLikeSnapshot(input) {
		return model.ConversationTurnContextSnapshot
	}
	return model.ConversationTurnContextExplicit
}

func canonicalConversationInputLooksLikeSnapshot(items []canonical.Item) bool {
	userMessages := 0
	for _, item := range items {
		if item.Type == "message" {
			switch item.Role {
			case canonical.RoleUser:
				userMessages++
			case canonical.RoleAssistant:
				return true
			}
		}
		if item.Type == "function_call" || item.Type == "reasoning" {
			return true
		}
	}
	return userMessages > 1
}

func loadOwnedConversationForUpdateTx(tx *gorm.DB, conversationID, userID, tokenID uint) (*model.Conversation, error) {
	var conversation model.Conversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ? AND token_id = ? AND status = 1", conversationID, userID, tokenID).
		First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &conversation, nil
}

// resolvePreviousResponseConversationTx accepts either the latest upstream
// provider response ID or any public response resource already projected into
// the conversation. Ambiguous identifiers are deliberately ignored.
func resolvePreviousResponseConversationTx(tx *gorm.DB, responseID string, userID, tokenID uint) (*model.Conversation, bool, error) {
	type candidate struct {
		model.Conversation
	}
	var candidates []candidate
	err := tx.Table("conversations AS conversation").
		Select("conversation.*").
		Joins("LEFT JOIN api_calls AS api_call ON api_call.conversation_id = conversation.id AND api_call.id = conversation.call_id").
		Where("conversation.user_id = ? AND conversation.token_id = ? AND conversation.status = 1", userID, tokenID).
		Where(`conversation.provider_response_id = ? OR api_call.resource_id = ? OR EXISTS (
			SELECT 1
			FROM conversation_turns AS response_turn
			JOIN ai_responses AS response ON response.call_id = response_turn.call_id
			WHERE response_turn.conversation_id = conversation.id
				AND response.id = ?
				AND response.user_id = conversation.user_id
				AND response.token_id = conversation.token_id
		)`, responseID, responseID, responseID).
		Limit(2).Scan(&candidates).Error
	if err != nil {
		return nil, false, err
	}
	if len(candidates) != 1 {
		if len(candidates) == 0 {
			pending, callID, pendingErr := pendingPublicResponseProjectionTx(tx, responseID, userID, tokenID)
			if pendingErr != nil {
				return nil, false, pendingErr
			}
			if pending {
				return nil, false, fmt.Errorf("%w: response %s call %s", ErrConversationProjectionDependencyPending, responseID, callID)
			}
		}
		return nil, false, nil
	}
	conversation, err := loadOwnedConversationForUpdateTx(tx, candidates[0].ID, userID, tokenID)
	if err != nil {
		return nil, false, err
	}
	return conversation, true, nil
}

func pendingPublicResponseProjectionTx(tx *gorm.DB, responseID string, userID, tokenID uint) (bool, string, error) {
	if !strings.HasPrefix(responseID, "resp_") {
		return false, "", nil
	}
	var response model.AIResponse
	err := tx.Select("call_id").
		Where("id = ? AND user_id = ? AND token_id = ?", responseID, userID, tokenID).
		First(&response).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	callID := strings.TrimSpace(response.CallID)
	if callID == "" {
		return false, "", nil
	}
	var turnCount int64
	if err := tx.Model(&model.ConversationTurn{}).Where("call_id = ?", callID).Count(&turnCount).Error; err != nil {
		return false, "", err
	}
	if turnCount > 0 {
		return false, callID, nil
	}
	var pendingCount int64
	if err := tx.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", callID).Count(&pendingCount).Error; err != nil {
		return false, "", err
	}
	return pendingCount > 0, callID, nil
}

func trimCanonicalConversationPrefixTx(
	tx *gorm.DB,
	conversation *model.Conversation,
	input []canonical.Item,
) ([]canonical.Item, bool, error) {
	consumed, _, matches, err := canonicalConversationPrefixForLockedConversationTx(tx, conversation, input)
	if err != nil {
		return nil, false, err
	}
	if matches {
		return canonical.CloneItems(input[consumed:]), true, nil
	}
	return canonical.CloneItems(input), false, nil
}

func prepareCanonicalConversationInputTx(
	tx *gorm.DB,
	conversation *model.Conversation,
	input []canonical.Item,
) ([]canonical.Item, bool, error) {
	if conversation == nil {
		return canonical.CloneItems(input), false, nil
	}
	if conversation.CanonicalStateVersion == 1 && conversation.CanonicalItemCount == 0 && conversation.CanonicalMatchHash == "" {
		return canonical.CloneItems(input), false, nil
	}
	if conversation.CanonicalStateVersion == 1 && conversation.CanonicalItemCount > 0 && conversation.CanonicalMatchHash != "" {
		// 新会话保存滚动哈希，可直接验证请求前缀；只有分叉历史才需要扫描数据库寻找公共前缀。
		consumed, matches := canonicalConversationPrefixFromState(
			input, conversation.CanonicalItemCount, conversation.CanonicalMatchHash,
		)
		if matches {
			if consumed == len(input) {
				repeated, found, err := completedCanonicalConversationTailInputTx(tx, conversation.ID, input)
				if err != nil {
					return nil, false, err
				}
				if found {
					return repeated, true, nil
				}
			}
			return canonical.CloneItems(input[consumed:]), true, nil
		}
		common, err := scanCanonicalConversationCommonPrefixTx(tx, conversation.ID, input)
		if err != nil {
			return nil, false, err
		}
		return trimPreparedCanonicalConversationCommonPrefix(input, common), common.commonItemCount > 0, nil
	}

	// 旧数据没有匹配状态，首次续话时完整扫描一次并回填，后续即可走上面的快速路径。
	scan, err := scanCanonicalConversationHistoryTx(tx, conversation.ID, input)
	if err != nil {
		return nil, false, err
	}
	if err := backfillCanonicalConversationStateTx(tx, conversation, scan); err != nil {
		return nil, false, err
	}
	if scan.matchesInput {
		if scan.inputConsumed == len(input) {
			repeated, found, err := completedCanonicalConversationTailInputTx(tx, conversation.ID, input)
			if err != nil {
				return nil, false, err
			}
			if found {
				return repeated, true, nil
			}
		}
		return canonical.CloneItems(input[scan.inputConsumed:]), true, nil
	}
	if scan.commonItemCount == 0 {
		return canonical.CloneItems(input), false, nil
	}
	return trimPreparedCanonicalConversationCommonPrefix(input, canonicalConversationCommonPrefixScan{
		inputItemCount:       scan.inputItemCount,
		commonItemCount:      scan.commonItemCount,
		commonInputConsumed:  scan.commonInputConsumed,
		lastCommonInputStart: scan.lastCommonInputStart,
		preserveInputStart:   scan.preserveInputStart,
		preserveInput:        scan.preserveInput,
		storedHasMore:        scan.commonStoredHasMore,
	}), true, nil
}

type canonicalConversationCommonPrefixScan struct {
	inputItemCount       uint64
	commonItemCount      uint64
	commonInputConsumed  int
	lastCommonInputStart int
	lastCommonTurnID     uint64
	lastCommonDirection  string
	preserveInputStart   int
	preserveInput        bool
	storedHasMore        bool
}

func scanCanonicalConversationCommonPrefixTx(
	tx *gorm.DB,
	conversationID uint,
	input []canonical.Item,
) (canonicalConversationCommonPrefixScan, error) {
	// 公共前缀用于处理客户端从较早轮次分叉的请求，只丢弃数据库与请求完全相同的历史部分。
	var scan canonicalConversationCommonPrefixScan
	inputFingerprints, inputPositions, valid := canonicalConversationMatchFingerprints(input)
	if !valid || len(inputFingerprints) == 0 {
		return scan, nil
	}
	scan.inputItemCount = uint64(len(inputFingerprints))
	inputExhausted := false
	_, _, err := scanCompletedCanonicalConversationFingerprintsTx(
		tx, conversationID, false,
		func(_ *model.ConversationItem) (bool, error) {
			if inputExhausted {
				scan.storedHasMore = true
				return false, nil
			}
			return true, nil
		},
		func(stored canonicalConversationStoredFingerprint) (bool, error) {
			if scan.commonItemCount == scan.inputItemCount {
				scan.storedHasMore = true
				return false, nil
			}
			inputIndex := scan.commonItemCount
			if !bytes.Equal(stored.fingerprint, inputFingerprints[inputIndex]) {
				return false, nil
			}
			scan.commonItemCount++
			scan.lastCommonTurnID = stored.turnID
			scan.lastCommonDirection = stored.direction
			if stored.direction == model.ConversationItemInput && stored.firstInDirection {
				scan.lastCommonInputStart = inputPositions[inputIndex]
			}
			if scan.commonItemCount == scan.inputItemCount {
				inputExhausted = true
			}
			return true, nil
		},
	)
	if err != nil || scan.commonItemCount == 0 {
		return scan, err
	}
	scan.commonInputConsumed = len(input)
	if scan.commonItemCount < uint64(len(inputPositions)) {
		scan.commonInputConsumed = inputPositions[scan.commonItemCount]
	}
	if scan.commonItemCount == scan.inputItemCount && scan.storedHasMore &&
		scan.lastCommonDirection == model.ConversationItemInput {
		// 请求在一轮输入的中间结束时保留整段输入，避免把同一用户动作拆成两个不完整轮次。
		boundary, err := canonicalConversationTurnInputBoundaryTx(
			tx, scan.lastCommonTurnID, input, scan.lastCommonInputStart,
		)
		if err != nil {
			return canonicalConversationCommonPrefixScan{}, err
		}
		if boundary.hasInput {
			scan.preserveInputStart = boundary.start
			scan.preserveInput = true
		}
	}
	return scan, nil
}

func trimPreparedCanonicalConversationCommonPrefix(
	input []canonical.Item,
	scan canonicalConversationCommonPrefixScan,
) []canonical.Item {
	if scan.commonItemCount == 0 {
		return canonical.CloneItems(input)
	}
	consumed := scan.commonInputConsumed
	if scan.preserveInput {
		consumed = scan.preserveInputStart
	}
	return canonical.CloneItems(input[consumed:])
}

type canonicalConversationTurnInputBoundary struct {
	start     int
	hasInput  bool
	hasOutput bool
}

func completedCanonicalConversationTailInputTx(
	tx *gorm.DB,
	conversationID uint,
	input []canonical.Item,
) ([]canonical.Item, bool, error) {
	var turn model.ConversationTurn
	err := tx.Select("id").
		Where("conversation_id = ? AND status = ?", conversationID, model.ConversationTurnCompleted).
		Order("turn_sequence DESC").Order("id DESC").First(&turn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	boundary, err := canonicalConversationTurnInputBoundaryTx(tx, turn.ID, input, -1)
	if err != nil {
		return nil, false, err
	}
	if !boundary.hasInput || boundary.hasOutput || boundary.start < 0 || boundary.start > len(input) {
		return nil, false, nil
	}
	return canonical.CloneItems(input[boundary.start:]), true, nil
}

func canonicalConversationTurnInputBoundaryTx(
	tx *gorm.DB,
	turnID uint64,
	input []canonical.Item,
	fallback int,
) (canonicalConversationTurnInputBoundary, error) {
	boundary := canonicalConversationTurnInputBoundary{start: fallback}
	var records []model.ConversationItem
	if err := tx.Where("turn_id = ?", turnID).
		Order("ordinal ASC").Order("id ASC").Find(&records).Error; err != nil {
		return canonicalConversationTurnInputBoundary{}, err
	}
	turnInput := make([]canonical.Item, 0, len(records))
	for index := range records {
		record := &records[index]
		if record.Direction == model.ConversationItemOutput {
			boundary.hasOutput = true
			continue
		}
		if record.Direction != model.ConversationItemInput {
			continue
		}
		var item canonical.Item
		if err := json.Unmarshal(record.CanonicalJSON, &item); err != nil {
			return canonicalConversationTurnInputBoundary{}, fmt.Errorf("decode conversation item %d: %w", record.ID, err)
		}
		turnInput = append(turnInput, item)
	}
	boundary.hasInput = len(turnInput) > 0
	if !boundary.hasInput {
		return boundary, nil
	}

	if len(turnInput) <= len(input) {
		candidate := len(input) - len(turnInput)
		equal, err := canonicalConversationItemSlicesEqual(turnInput, input[candidate:])
		if err != nil {
			return canonicalConversationTurnInputBoundary{}, err
		}
		if equal {
			boundary.start = candidate
			return boundary, nil
		}
	}

	turnFingerprints, _, turnValid := canonicalConversationMatchFingerprints(turnInput)
	inputFingerprints, inputPositions, inputValid := canonicalConversationMatchFingerprints(input)
	if !turnValid || !inputValid || len(turnFingerprints) == 0 || len(turnFingerprints) > len(inputFingerprints) {
		return boundary, nil
	}
	offset := len(inputFingerprints) - len(turnFingerprints)
	if canonicalConversationFingerprintPrefixEqual(turnFingerprints, inputFingerprints[offset:]) {
		boundary.start = inputPositions[offset]
	}
	return boundary, nil
}

func canonicalConversationItemSlicesEqual(left, right []canonical.Item) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	for index := range left {
		leftJSON, err := marshalConversationCanonicalItem(left[index])
		if err != nil {
			return false, err
		}
		rightJSON, err := marshalConversationCanonicalItem(right[index])
		if err != nil {
			return false, err
		}
		if !bytes.Equal(leftJSON, rightJSON) {
			return false, nil
		}
	}
	return true, nil
}

func canonicalConversationPrefixForLockedConversationTx(
	tx *gorm.DB,
	conversation *model.Conversation,
	input []canonical.Item,
) (int, int, bool, error) {
	if conversation == nil {
		return 0, 0, false, nil
	}
	if conversation.CanonicalStateVersion == 1 && conversation.CanonicalItemCount == 0 && conversation.CanonicalMatchHash == "" {
		return 0, 0, false, nil
	}
	if conversation.CanonicalStateVersion == 1 && conversation.CanonicalItemCount > 0 && conversation.CanonicalMatchHash != "" {
		consumed, matches := canonicalConversationPrefixFromState(
			input, conversation.CanonicalItemCount, conversation.CanonicalMatchHash,
		)
		return consumed, int(conversation.CanonicalItemCount), matches, nil
	}
	// Existing rows created before match-state tracking are scanned once on an
	// explicit continuation. New automatic matching never selects legacy rows.
	scan, err := scanCanonicalConversationHistoryTx(tx, conversation.ID, input)
	if err != nil {
		return 0, 0, false, err
	}
	if err := backfillCanonicalConversationStateTx(tx, conversation, scan); err != nil {
		return 0, 0, false, err
	}
	return scan.inputConsumed, int(scan.itemCount), scan.matchesInput, nil
}

type canonicalConversationHistoryScan struct {
	itemCount            uint64
	totalBytes           uint64
	matchHash            string
	inputItemCount       uint64
	inputConsumed        int
	commonItemCount      uint64
	commonInputConsumed  int
	lastCommonInputStart int
	lastCommonTurnID     uint64
	lastCommonDirection  string
	preserveInputStart   int
	preserveInput        bool
	commonStoredHasMore  bool
	matchesInput         bool
}

func scanCanonicalConversationHistoryTx(
	tx *gorm.DB,
	conversationID uint,
	input []canonical.Item,
) (canonicalConversationHistoryScan, error) {
	// 一次顺序扫描同时计算滚动哈希、完整前缀匹配和最长公共前缀，减少旧会话升级时的查询次数。
	var scan canonicalConversationHistoryScan
	inputFingerprints, inputPositions, inputValid := canonicalConversationMatchFingerprints(input)
	scan.inputItemCount = uint64(len(inputFingerprints))
	var commonPrefixCount uint64
	var previousHash []byte
	inputExhausted := false

	totalBytes, _, err := scanCompletedCanonicalConversationFingerprintsTx(
		tx, conversationID, true,
		func(_ *model.ConversationItem) (bool, error) {
			if inputExhausted {
				scan.commonStoredHasMore = true
			}
			return true, nil
		},
		func(stored canonicalConversationStoredFingerprint) (bool, error) {
			if commonPrefixCount == scan.itemCount && scan.itemCount < uint64(len(inputFingerprints)) &&
				bytes.Equal(stored.fingerprint, inputFingerprints[scan.itemCount]) {
				inputIndex := scan.itemCount
				commonPrefixCount++
				scan.lastCommonTurnID = stored.turnID
				scan.lastCommonDirection = stored.direction
				if stored.direction == model.ConversationItemInput && stored.firstInDirection {
					scan.lastCommonInputStart = inputPositions[inputIndex]
				}
				if commonPrefixCount == uint64(len(inputFingerprints)) {
					inputExhausted = true
				}
			} else if inputExhausted {
				scan.commonStoredHasMore = true
			}
			hash := sha256.New()
			_, _ = hash.Write(previousHash)
			_, _ = hash.Write(stored.fingerprint)
			previousHash = hash.Sum(previousHash[:0])
			scan.itemCount++
			return true, nil
		},
	)
	if err != nil {
		return canonicalConversationHistoryScan{}, err
	}
	scan.totalBytes = totalBytes
	if len(previousHash) > 0 {
		scan.matchHash = hex.EncodeToString(previousHash)
	}
	if scan.itemCount == 0 || !inputValid {
		return scan, nil
	}
	if commonPrefixCount > 0 {
		scan.commonItemCount = commonPrefixCount
		scan.commonInputConsumed = len(input)
		if commonPrefixCount < uint64(len(inputPositions)) {
			scan.commonInputConsumed = inputPositions[commonPrefixCount]
		}
	}
	if commonPrefixCount == uint64(len(inputFingerprints)) && scan.commonStoredHasMore &&
		scan.lastCommonDirection == model.ConversationItemInput {
		boundary, err := canonicalConversationTurnInputBoundaryTx(
			tx, scan.lastCommonTurnID, input, scan.lastCommonInputStart,
		)
		if err != nil {
			return canonicalConversationHistoryScan{}, err
		}
		if boundary.hasInput {
			scan.preserveInputStart = boundary.start
			scan.preserveInput = true
		}
	}
	if scan.itemCount <= uint64(len(inputFingerprints)) && commonPrefixCount == scan.itemCount {
		scan.matchesInput = true
		scan.inputConsumed = len(input)
		if scan.itemCount < uint64(len(inputPositions)) {
			scan.inputConsumed = inputPositions[scan.itemCount]
		}
	}
	return scan, nil
}

func backfillCanonicalConversationStateTx(
	tx *gorm.DB,
	conversation *model.Conversation,
	scan canonicalConversationHistoryScan,
) error {
	if conversation == nil || conversation.CanonicalStateVersion == 1 {
		return nil
	}
	updates := map[string]any{
		"canonical_item_count":    scan.itemCount,
		"canonical_bytes":         scan.totalBytes,
		"canonical_match_hash":    scan.matchHash,
		"canonical_state_version": 1,
	}
	result := tx.Model(&model.Conversation{}).
		Where("id = ? AND canonical_state_version = 0", conversation.ID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	conversation.CanonicalItemCount = scan.itemCount
	conversation.CanonicalBytes = scan.totalBytes
	conversation.CanonicalMatchHash = scan.matchHash
	conversation.CanonicalStateVersion = 1
	return nil
}

func matchCanonicalConversationPrefixTx(tx *gorm.DB, userID, tokenID uint, input []canonical.Item) (*model.Conversation, int, bool, error) {
	if len(input) == 0 {
		return nil, 0, false, nil
	}
	inputFingerprints, _, ok := canonicalConversationMatchFingerprints(input)
	if !ok {
		return nil, 0, false, nil
	}
	inputHashes := canonicalConversationRollingHashes("", inputFingerprints)
	var conversations []model.Conversation
	if err := tx.Where("user_id = ? AND token_id = ? AND status = 1", userID, tokenID).
		Order("updated_at DESC").Order("id DESC").Limit(canonicalConversationMatchLimit).
		Find(&conversations).Error; err != nil {
		return nil, 0, false, err
	}
	if len(conversations) == 0 {
		return nil, 0, false, nil
	}
	// 选择最长匹配前缀；同长度命中多个会话时放弃自动续话，防止静默关联到错误会话。
	bestLength := 0
	bestIndex := -1
	ambiguous := false
	for index := range conversations {
		conversation := &conversations[index]
		length := int(conversation.CanonicalItemCount)
		if conversation.CanonicalStateVersion != 1 || length == 0 || conversation.CanonicalMatchHash == "" || length < bestLength ||
			length > len(inputHashes) || inputHashes[length-1] != conversation.CanonicalMatchHash {
			continue
		}
		if length == bestLength {
			ambiguous = true
			continue
		}
		bestLength = length
		bestIndex = index
		ambiguous = false
	}
	if bestIndex < 0 || ambiguous {
		return nil, 0, false, nil
	}
	return &conversations[bestIndex], bestLength, true, nil
}

type canonicalConversationStoredFingerprint struct {
	fingerprint      []byte
	turnID           uint64
	direction        string
	firstInDirection bool
}

func scanCompletedCanonicalConversationFingerprintsTx(
	tx *gorm.DB,
	conversationID uint,
	measureBytes bool,
	observe func(*model.ConversationItem) (bool, error),
	visit func(canonicalConversationStoredFingerprint) (bool, error),
) (uint64, bool, error) {
	// 使用键集分页读取稳定顺序，避免大会话一次载入内存，也避免 OFFSET 在并发写入下漂移。
	type pendingFingerprint struct {
		fingerprint []byte
		turnID      uint64
		direction   string
	}
	var (
		totalBytes            uint64
		lastTurnSequence      uint64
		lastOrdinal           int
		lastID                uint64
		hasCursor             bool
		pendingShell          *pendingFingerprint
		pendingTrailingRecord *model.ConversationItem
		lastEmittedTurnID     uint64
		lastEmittedDirection  string
		hasEmittedDirection   bool
	)
	emit := func(pending pendingFingerprint) (bool, error) {
		stored := canonicalConversationStoredFingerprint{
			fingerprint: pending.fingerprint,
			turnID:      pending.turnID,
			direction:   pending.direction,
			firstInDirection: !hasEmittedDirection || lastEmittedTurnID != pending.turnID ||
				lastEmittedDirection != pending.direction,
		}
		lastEmittedTurnID = pending.turnID
		lastEmittedDirection = pending.direction
		hasEmittedDirection = true
		if visit == nil {
			return true, nil
		}
		return visit(stored)
	}
	observeRecord := func(record *model.ConversationItem) (bool, error) {
		if observe == nil {
			return true, nil
		}
		return observe(record)
	}

	for {
		var records []model.ConversationItem
		query := tx.Model(&model.ConversationItem{}).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("conversation_items.*").
			Joins("JOIN conversation_turns ON conversation_turns.id = conversation_items.turn_id").
			Where("conversation_items.conversation_id = ? AND conversation_turns.status = ?", conversationID, model.ConversationTurnCompleted)
		if hasCursor {
			query = query.Where(`
				conversation_items.turn_sequence > ? OR
				(conversation_items.turn_sequence = ? AND conversation_items.ordinal > ?) OR
				(conversation_items.turn_sequence = ? AND conversation_items.ordinal = ? AND conversation_items.id > ?)`,
				lastTurnSequence,
				lastTurnSequence, lastOrdinal,
				lastTurnSequence, lastOrdinal, lastID,
			)
		}
		if err := query.
			Order("conversation_items.turn_sequence ASC").
			Order("conversation_items.ordinal ASC").
			Order("conversation_items.id ASC").
			Limit(canonicalConversationScanPageSize).
			Find(&records).Error; err != nil {
			return 0, false, err
		}
		if len(records) == 0 {
			break
		}

		for index := range records {
			record := &records[index]
			lastTurnSequence = record.TurnSequence
			lastOrdinal = record.Ordinal
			lastID = record.ID
			hasCursor = true

			var item canonical.Item
			if err := json.Unmarshal(record.CanonicalJSON, &item); err != nil {
				return 0, false, fmt.Errorf("decode conversation item %d: %w", record.ID, err)
			}
			if measureBytes {
				encoded, err := marshalConversationCanonicalItem(item)
				if err != nil {
					return 0, false, fmt.Errorf("encode conversation item %d: %w", record.ID, err)
				}
				totalBytes += uint64(len(encoded))
			}
			if item.Type == "reasoning" {
				// reasoning 不参与历史身份匹配，但仍计入观察结果；不同 Provider 可省略或改写它。
				proceed, err := observeRecord(record)
				if err != nil || !proceed {
					return totalBytes, false, err
				}
				if pendingShell != nil {
					copy := *record
					pendingTrailingRecord = &copy
				}
				continue
			}
			if pendingShell != nil {
				if item.Type != "function_call" {
					proceed, err := emit(*pendingShell)
					if err != nil || !proceed {
						return totalBytes, false, err
					}
				}
				pendingShell = nil
				pendingTrailingRecord = nil
			}
			proceed, err := observeRecord(record)
			if err != nil || !proceed {
				return totalBytes, false, err
			}
			if canonicalConversationPotentialToolShell(item) {
				// Chat 协议可能在 function_call 前生成空 assistant message；暂存后看下一项再决定是否匹配。
				fingerprint, err := canonicalConversationFingerprint(item)
				if err != nil {
					return 0, false, fmt.Errorf("fingerprint conversation item %d: %w", record.ID, err)
				}
				pendingShell = &pendingFingerprint{
					fingerprint: fingerprint,
					turnID:      record.TurnID,
					direction:   record.Direction,
				}
				continue
			}
			fingerprint, err := canonicalConversationFingerprint(item)
			if err != nil {
				return 0, false, fmt.Errorf("fingerprint conversation item %d: %w", record.ID, err)
			}
			proceed, err = emit(pendingFingerprint{
				fingerprint: fingerprint,
				turnID:      record.TurnID,
				direction:   record.Direction,
			})
			if err != nil || !proceed {
				return totalBytes, false, err
			}
		}
		if len(records) < canonicalConversationScanPageSize {
			break
		}
	}
	if pendingShell != nil {
		proceed, err := emit(*pendingShell)
		if err != nil || !proceed {
			return totalBytes, false, err
		}
		if pendingTrailingRecord != nil {
			proceed, err = observeRecord(pendingTrailingRecord)
			if err != nil || !proceed {
				return totalBytes, false, err
			}
		}
	}
	return totalBytes, true, nil
}

func loadCompletedCanonicalConversationItemsTx(tx *gorm.DB, conversationIDs []uint) (map[uint][]canonical.Item, error) {
	return loadCompletedCanonicalConversationItems(tx, conversationIDs)
}

func loadCompletedCanonicalConversationItems(tx *gorm.DB, conversationIDs []uint) (map[uint][]canonical.Item, error) {
	result := make(map[uint][]canonical.Item, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}
	var records []model.ConversationItem
	query := tx.Model(&model.ConversationItem{})
	err := query.
		Select("conversation_items.*").
		Joins("JOIN conversation_turns ON conversation_turns.id = conversation_items.turn_id").
		Where("conversation_items.conversation_id IN ? AND conversation_turns.status = ?", conversationIDs, model.ConversationTurnCompleted).
		Order("conversation_items.conversation_id ASC").
		Order("conversation_items.turn_sequence ASC").
		Order("conversation_items.ordinal ASC").
		Order("conversation_items.id ASC").Find(&records).Error
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		var item canonical.Item
		if err := json.Unmarshal(record.CanonicalJSON, &item); err != nil {
			return nil, fmt.Errorf("decode conversation item %d: %w", record.ID, err)
		}
		result[record.ConversationID] = append(result[record.ConversationID], item)
	}
	return result, nil
}

func createCanonicalConversationTx(tx *gorm.DB, userID, tokenID uint, modelCode string, items []canonical.Item) (*model.Conversation, error) {
	conversation := &model.Conversation{
		UserID: userID, TokenID: tokenID, Model: modelCode,
		Title: canonicalConversationTitle(items), SystemPrompt: canonicalConversationSystemPrompt(items),
		LastStatus: "pending", CanonicalStateVersion: 1, Status: 1,
	}
	if err := tx.Create(conversation).Error; err != nil {
		return nil, err
	}
	return conversation, nil
}

func createCanonicalConversationItemsTx(tx *gorm.DB, turn *model.ConversationTurn, input, output []canonical.Item) (uint64, error) {
	records := make([]model.ConversationItem, 0, len(input)+len(output))
	var totalBytes uint64
	appendItems := func(direction string, items []canonical.Item) error {
		for _, item := range items {
			encoded, err := marshalConversationCanonicalItem(item)
			if err != nil {
				return err
			}
			totalBytes += uint64(len(encoded))
			records = append(records, model.ConversationItem{
				ConversationID: turn.ConversationID, TurnID: turn.ID, TurnSequence: turn.Sequence,
				Direction: direction, Ordinal: len(records), CanonicalJSON: encoded,
			})
		}
		return nil
	}
	if err := appendItems(model.ConversationItemInput, input); err != nil {
		return 0, err
	}
	if err := appendItems(model.ConversationItemOutput, output); err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	return totalBytes, tx.Create(&records).Error
}

func countCanonicalConversationMessages(items []canonical.Item) int {
	count := 0
	functionCallGroupHasMessage := false
	for _, item := range items {
		switch item.Type {
		case "message":
			count++
			functionCallGroupHasMessage = item.Role == canonical.RoleAssistant
		case "function_call":
			if !functionCallGroupHasMessage {
				count++
				functionCallGroupHasMessage = true
			}
		case "function_call_output":
			count++
			functionCallGroupHasMessage = false
		case "reasoning":
			// Reasoning belongs to the adjacent assistant action.
		default:
			count++
			functionCallGroupHasMessage = false
		}
	}
	return count
}

func canonicalConversationPrefixEqual(prefix, input []canonical.Item) bool {
	_, _, matches := canonicalConversationPrefixConsumed(prefix, input)
	return matches
}

func canonicalConversationPrefixConsumed(prefix, input []canonical.Item) (int, int, bool) {
	prefixFingerprints, _, prefixOK := canonicalConversationMatchFingerprints(prefix)
	inputFingerprints, inputPositions, inputOK := canonicalConversationMatchFingerprints(input)
	if !prefixOK || !inputOK || len(prefixFingerprints) == 0 ||
		!canonicalConversationFingerprintPrefixEqual(prefixFingerprints, inputFingerprints) {
		return 0, 0, false
	}
	consumed := len(input)
	if len(prefixFingerprints) < len(inputPositions) {
		consumed = inputPositions[len(prefixFingerprints)]
	}
	return consumed, len(prefixFingerprints), true
}

func canonicalConversationPrefixFromState(input []canonical.Item, count uint64, expectedHash string) (int, bool) {
	if count == 0 || expectedHash == "" || count > uint64(len(input)) {
		return 0, false
	}
	fingerprints, positions, ok := canonicalConversationMatchFingerprints(input)
	if !ok || count > uint64(len(fingerprints)) {
		return 0, false
	}
	hashes := canonicalConversationRollingHashes("", fingerprints[:count])
	if len(hashes) == 0 || hashes[len(hashes)-1] != expectedHash {
		return 0, false
	}
	consumed := len(input)
	if int(count) < len(positions) {
		consumed = positions[count]
	}
	return consumed, true
}

func canonicalConversationRollingHashes(initial string, fingerprints [][]byte) []string {
	// H[n] = SHA256(H[n-1] || fingerprint[n])，可用固定大小状态验证任意已保存前缀。
	var previous []byte
	if initial != "" {
		decoded, err := hex.DecodeString(initial)
		if err != nil || len(decoded) != sha256.Size {
			return nil
		}
		previous = decoded
	}
	result := make([]string, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		payload := make([]byte, 0, len(previous)+len(fingerprint))
		payload = append(payload, previous...)
		payload = append(payload, fingerprint...)
		digest := sha256.Sum256(payload)
		previous = append(previous[:0], digest[:]...)
		result = append(result, hex.EncodeToString(digest[:]))
	}
	return result
}

func canonicalConversationFingerprintPrefixEqual(prefix, input [][]byte) bool {
	if len(prefix) == 0 || len(prefix) > len(input) {
		return false
	}
	for index := range prefix {
		if !bytes.Equal(prefix[index], input[index]) {
			return false
		}
	}
	return true
}

func canonicalConversationMatchFingerprints(items []canonical.Item) ([][]byte, []int, bool) {
	// positions 保留指纹到原切片的映射，因为 reasoning 等可忽略项不会进入指纹序列。
	result := make([][]byte, 0, len(items))
	positions := make([]int, 0, len(items))
	for index := range items {
		if canonicalConversationMatchIgnorable(items, index) {
			continue
		}
		fingerprint, err := canonicalConversationFingerprint(items[index])
		if err != nil {
			return nil, nil, false
		}
		result = append(result, fingerprint)
		positions = append(positions, index)
	}
	return result, positions, true
}

func canonicalConversationMatchIgnorable(items []canonical.Item, index int) bool {
	if index < 0 || index >= len(items) {
		return false
	}
	item := items[index]
	if item.Type == "reasoning" {
		return true
	}
	if !canonicalConversationPotentialToolShell(item) {
		return false
	}
	for next := index + 1; next < len(items); next++ {
		if items[next].Type == "reasoning" {
			continue
		}
		return items[next].Type == "function_call"
	}
	return false
}

func canonicalConversationPotentialToolShell(item canonical.Item) bool {
	if item.Type != "message" || item.Role != canonical.RoleAssistant || item.Name != "" ||
		len(item.Arguments) > 0 || len(item.Output) > 0 {
		return false
	}
	for _, content := range item.Content {
		if content.Text != "" || content.URL != "" || content.Data != "" || content.FileID != "" ||
			content.Filename != "" || content.Transcript != "" || len(content.Extra) > 0 {
			return false
		}
	}
	for key := range item.Extra {
		switch key {
		case "openai_chat.content_mode", "openai_chat.reasoning_content", "prism.choice_index":
		default:
			return false
		}
	}
	return true
}

type canonicalConversationComparableItem struct {
	Type      string                                   `json:"type"`
	Role      canonical.Role                           `json:"role,omitempty"`
	Name      string                                   `json:"name,omitempty"`
	CallID    string                                   `json:"call_id,omitempty"`
	Content   []canonicalConversationComparableContent `json:"content,omitempty"`
	Arguments any                                      `json:"arguments,omitempty"`
	Output    any                                      `json:"output,omitempty"`
	Extra     map[string]any                           `json:"extra,omitempty"`
}

type canonicalConversationComparableContent struct {
	Type       string         `json:"type"`
	Text       string         `json:"text,omitempty"`
	URL        string         `json:"url,omitempty"`
	Data       string         `json:"data,omitempty"`
	FileID     string         `json:"file_id,omitempty"`
	Filename   string         `json:"filename,omitempty"`
	MediaType  string         `json:"media_type,omitempty"`
	Format     string         `json:"format,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Transcript string         `json:"transcript,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

func canonicalConversationFingerprint(item canonical.Item) ([]byte, error) {
	comparable := canonicalConversationComparableItem{
		Type: item.Type, Role: item.Role, Name: item.Name, CallID: item.CallID,
	}
	if item.Type == "message" && comparable.Role == "" {
		comparable.Role = canonical.RoleUser
	}
	for _, content := range item.Content {
		part := canonicalConversationComparableContent{
			Type: normalizeCanonicalConversationContentType(content.Type), Text: content.Text,
			URL: content.URL, Data: content.Data, FileID: content.FileID, Filename: content.Filename,
			MediaType: content.MediaType, Format: content.Format, Detail: content.Detail, Transcript: content.Transcript,
		}
		if !isKnownCanonicalConversationContentType(content.Type) {
			part.Extra = canonicalConversationComparableExtra(content.Extra)
		}
		comparable.Content = append(comparable.Content, part)
	}
	if !isKnownCanonicalConversationItemType(item.Type) {
		comparable.Extra = canonicalConversationComparableExtra(item.Extra)
	}
	var err error
	if len(item.Arguments) > 0 {
		comparable.Arguments, err = canonicalConversationJSONValue(item.Arguments)
		if err != nil {
			return nil, err
		}
	}
	if len(item.Output) > 0 {
		comparable.Output, err = canonicalConversationJSONValue(item.Output)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(comparable)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func canonicalConversationComparableExtra(extra map[string]json.RawMessage) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	result := make(map[string]any, len(extra))
	for key, raw := range extra {
		value, _ := canonicalConversationJSONValue(raw)
		result[key] = value
	}
	return result
}

func isKnownCanonicalConversationItemType(itemType string) bool {
	switch itemType {
	case "message", "function_call", "function_call_output", "reasoning":
		return true
	default:
		return false
	}
}

func isKnownCanonicalConversationContentType(contentType string) bool {
	switch normalizeCanonicalConversationContentType(contentType) {
	case "text", "image", "file", "audio", "video":
		return true
	default:
		return false
	}
}

func canonicalConversationJSONValue(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return string(raw), nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return string(raw), nil
	}
	return value, nil
}

func normalizeCanonicalConversationContentType(contentType string) string {
	switch contentType {
	case "", "input_text", "output_text", "text":
		return "text"
	case "input_image", "output_image", "image", "image_url":
		return "image"
	case "input_file", "output_file", "file", "document":
		return "file"
	case "input_audio", "output_audio", "audio":
		return "audio"
	case "input_video", "output_video", "video":
		return "video"
	default:
		return contentType
	}
}

func canonicalConversationTitle(items []canonical.Item) string {
	for _, item := range items {
		if item.Type == "message" && item.Role == canonical.RoleUser {
			if text := canonicalConversationItemText(item); text != "" {
				return truncateString(text, 50)
			}
		}
	}
	return ""
}

func canonicalConversationSystemPrompt(items []canonical.Item) string {
	for _, item := range items {
		if item.Type == "message" && item.Role == canonical.RoleSystem {
			if text := canonicalConversationItemText(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func canonicalConversationItemText(item canonical.Item) string {
	var text strings.Builder
	for _, content := range item.Content {
		if normalizeCanonicalConversationContentType(content.Type) == "text" {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}
