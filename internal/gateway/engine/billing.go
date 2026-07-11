package engine

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
)

const defaultOutput int64 = 4096

type Reservation struct {
	billing         *service.BillingService
	tokenID, userID uint
	route           *routing.RouteResult
	amount          decimal.Decimal
	key             string
}

func Reserve(b *service.BillingService, tokenID, userID uint, route *routing.RouteResult, req canonical.Request, key string) (*Reservation, error) {
	if b == nil {
		return nil, fmt.Errorf("billing service is required")
	}
	amount := estimate(route, req).RoundCeil(4)
	reserveKey, settleKey := "", ""
	if key != "" {
		reserveKey = key + ":reserve"
		settleKey = key + ":settle"
	}
	r := &Reservation{b, tokenID, userID, route, amount, settleKey}
	if amount.IsPositive() {
		if err := b.DeductWithKey(tokenID, userID, amount, reserveKey); err != nil {
			return nil, err
		}
	}
	return r, nil
}
func (r *Reservation) Cancel() error {
	if r == nil || !r.amount.IsPositive() {
		return nil
	}
	return r.billing.SettleReservation(r.tokenID, r.userID, r.amount, decimal.Zero, r.key)
}
func (r *Reservation) Settle(u *canonical.Usage) error {
	if r == nil || !r.amount.IsPositive() {
		return nil
	}
	if u == nil {
		return r.billing.SettleReservation(r.tokenID, r.userID, r.amount, decimal.Zero, r.key)
	}
	actual := cost(r.route, int64(u.InputTokens), int64(u.OutputTokens))
	return r.billing.SettleReservation(r.tokenID, r.userID, r.amount, actual.RoundCeil(4), r.key)
}
func estimate(route *routing.RouteResult, req canonical.Request) decimal.Decimal {
	if route == nil {
		return decimal.Zero
	}
	if route.PriceMode == "request" {
		return route.InputPrice
	}
	out := defaultOutput
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		out = int64(*req.MaxOutputTokens)
	}
	return cost(route, estimateInputTokens(req), out)
}

func estimateInputTokens(req canonical.Request) int64 {
	tokens := int64(8)
	tokens += estimateTextTokens(req.Instructions) + estimateTextTokens(req.User)
	for _, item := range req.Items {
		tokens += 4 + estimateTextTokens(string(item.Role)) + estimateTextTokens(item.Name)
		for _, content := range item.Content {
			tokens += 3 + estimateTextTokens(content.Text) + estimateTextTokens(content.Transcript)
			switch content.Type {
			case "input_image", "image", "image_url":
				tokens += 1024
			case "input_file", "file", "document", "input_audio", "audio", "input_video", "video":
				tokens += 2048
			}
		}
		tokens += estimateRawTokens(item.Arguments) + estimateRawTokens(item.Output)
	}
	for _, tool := range req.Tools {
		tokens += 8 + estimateTextTokens(tool.Name) + estimateTextTokens(tool.Description)
		tokens += estimateRawTokens(tool.InputSchema) + estimateRawTokens(tool.Options)
	}
	if req.ResponseFormat != nil {
		tokens += 8 + estimateTextTokens(req.ResponseFormat.Name) + estimateTextTokens(req.ResponseFormat.Description)
		tokens += estimateRawTokens(req.ResponseFormat.Schema)
	}
	for key, value := range req.Metadata {
		tokens += estimateTextTokens(key) + estimateTextTokens(value)
	}
	if tokens < 1 {
		return 1
	}
	return tokens
}

func estimateRawTokens(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	return estimateTextTokens(string(raw))
}

func estimateTextTokens(value string) int64 {
	if value == "" {
		return 0
	}
	var ascii, nonASCII int64
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII
}
func cost(route *routing.RouteResult, input, output int64) decimal.Decimal {
	if route.PriceMode == "request" {
		return route.InputPrice
	}
	return route.InputPrice.Mul(decimal.NewFromInt(input)).Add(route.OutputPrice.Mul(decimal.NewFromInt(output))).Div(decimal.NewFromInt(1000000))
}
