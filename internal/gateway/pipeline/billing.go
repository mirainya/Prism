package pipeline

import (
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// settleUsage 按实际 token 用量结算(Option A:无预扣,响应后按量扣)。
// 价格单位与老逻辑一致:price 为每百万 token 价 → cost = tokens * price / 1e6。
// idempotentKey 用 requestID,billing_logs 唯一索引兜底防重复扣。
func settleUsage(billing *service.BillingService, tokenID, userID uint, route *routing.RouteResult, usage *chat.ChatUsage, idempotentKey string) {
	if usage == nil || route == nil {
		return
	}
	inputCost := decimal.NewFromInt(int64(usage.PromptTokens)).
		Mul(route.InputPrice).
		Div(decimal.NewFromInt(1_000_000))
	outputCost := decimal.NewFromInt(int64(usage.CompletionTokens)).
		Mul(route.OutputPrice).
		Div(decimal.NewFromInt(1_000_000))
	total := inputCost.Add(outputCost)
	if !total.IsPositive() {
		return
	}
	if err := billing.DeductWithKey(tokenID, userID, total, idempotentKey); err != nil {
		logger.Warn("gw settle failed", zap.Uint("token_id", tokenID), zap.Error(err))
	}
}
