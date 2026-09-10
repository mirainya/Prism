package generic

import (
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// Read the JSON number's original text; Result.Float would lose price digits.
func firstMoney(payload []byte, paths []string) (*decimal.Decimal, error) {
	result := firstResult(payload, paths)
	if !result.Exists() {
		return nil, nil
	}
	var value string
	switch result.Type {
	case gjson.Number:
		value = result.Raw
	case gjson.String:
		value = strings.TrimSpace(result.Str)
	default:
		return nil, fmt.Errorf("invalid monetary value at %v", paths)
	}
	amount, err := billing.ParseAmount(value, 18, true)
	if err != nil {
		return nil, fmt.Errorf("invalid monetary value at %v: %w", paths, err)
	}
	if err := amount.EnsureRange(maxProviderMoney); err != nil {
		return nil, err
	}
	parsed, err := decimal.NewFromString(amount.String())
	return &parsed, err
}

var maxProviderMoney, _ = billing.ParseAmount("99999999999999999999", 0, true)
