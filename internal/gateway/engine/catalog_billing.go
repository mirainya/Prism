package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
)

func canonicalOperationPath(endpoint canonical.Endpoint) string {
	switch endpoint {
	case canonical.EndpointOpenAIChat:
		return "/v1/chat/completions"
	case canonical.EndpointOpenAIResponses:
		return "/v1/responses"
	case canonical.EndpointAnthropic:
		return "/v1/messages"
	default:
		return ""
	}
}

func canonicalSellSchedule(route *routing.RouteResult) (billing.RateSchedule, error) {
	if route == nil || route.SellSchedule == nil {
		return billing.RateSchedule{}, fmt.Errorf("%w: published sell schedule is required", billing.ErrInvalidRate)
	}
	schedule := *route.SellSchedule
	schedule.Components = append([]billing.RateComponent(nil), schedule.Components...)
	if err := schedule.Validate(); err != nil {
		return billing.RateSchedule{}, err
	}
	if route.Currency != schedule.Currency.Code || route.CurrencyVersion != uint(schedule.Currency.Version) {
		return billing.RateSchedule{}, fmt.Errorf("%w: route currency differs from the published schedule", billing.ErrInvalidRate)
	}
	for _, component := range schedule.Components {
		// This engine consumes canonical chat/Responses usage. Media and delivery
		// meters require their own execution facts, never an assumed quantity.
		if component.Event != billing.ChargeSucceeded {
			return billing.RateSchedule{}, fmt.Errorf("%w: canonical engine cannot establish event %s", billing.ErrInvalidRate, component.Event)
		}
		switch component.Source {
		case billing.QuantityOne, billing.QuantityInputTokens, billing.QuantityOutputTokens,
			billing.QuantityCachedTokens, billing.QuantityUncachedTokens:
		default:
			return billing.RateSchedule{}, fmt.Errorf("%w: canonical engine cannot measure %s", billing.ErrInvalidRate, component.Source)
		}
	}
	return schedule, nil
}

// CanonicalBillingFacts converts normalized chat/Responses usage into the
// billing facts consumed by both the synchronous engine and background
// Responses worker.
func CanonicalBillingFacts(usage *canonical.Usage) (billing.Facts, error) {
	declared := map[string]bool{
		"success":            true,
		"input_tokens":       true,
		"output_tokens":      true,
		"cache_read_tokens":  true,
		"cache_write_tokens": true,
		"reasoning_tokens":   true,
	}
	facts := billing.Facts{Events: map[billing.ChargeEvent]bool{
		billing.ChargeSucceeded: true, billing.ChargeFailed: false, billing.ChargeCancelled: false,
	}, Quantities: make(map[billing.QuantitySource]string), Expr: billing.ExprEnv{Declared: declared, Vars: make(map[string]billing.ExprValue, len(declared))}}
	putExprNumber := func(name, value string) error {
		amount, err := billing.ParseAmount(value, 18, true)
		if err != nil {
			return err
		}
		facts.Expr.Vars[name] = billing.ExprNumber(amount)
		return nil
	}
	if err := putExprNumber("success", "1"); err != nil {
		return billing.Facts{}, err
	}
	if usage == nil {
		return facts, nil
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens < 0 || usage.CachedInputTokens > usage.InputTokens {
		return billing.Facts{}, billing.ErrInvalidAmount
	}
	facts.Quantities[billing.QuantityInputTokens] = strconv.Itoa(usage.InputTokens)
	facts.Quantities[billing.QuantityOutputTokens] = strconv.Itoa(usage.OutputTokens)
	facts.Quantities[billing.QuantityCachedTokens] = strconv.Itoa(usage.CachedInputTokens)
	facts.Quantities[billing.QuantityUncachedTokens] = strconv.Itoa(usage.InputTokens - usage.CachedInputTokens)
	for name, value := range map[string]int{
		"input_tokens":      usage.InputTokens,
		"output_tokens":     usage.OutputTokens,
		"cache_read_tokens": usage.CachedInputTokens,
		"reasoning_tokens":  usage.ReasoningOutputTokens,
	} {
		if err := putExprNumber(name, strconv.Itoa(value)); err != nil {
			return billing.Facts{}, err
		}
	}
	if value, ok, err := canonicalExtraQuantity(usage.Extra, "cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens"); err != nil {
		return billing.Facts{}, err
	} else if ok {
		if err := putExprNumber("cache_write_tokens", value); err != nil {
			return billing.Facts{}, err
		}
	}
	return facts, nil
}

// Keep the package-local name for the existing engine tests and callers while
// the background worker uses the shared exported conversion above.
func canonicalBillingFacts(usage *canonical.Usage) (billing.Facts, error) {
	return CanonicalBillingFacts(usage)
}

func canonicalExtraQuantity(extra map[string]json.RawMessage, keys ...string) (string, bool, error) {
	for _, key := range keys {
		raw, ok := extra[key]
		if !ok || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		var number json.Number
		if err := decoder.Decode(&number); err == nil {
			var extra any
			if err := decoder.Decode(&extra); err != nil {
				if err != io.EOF {
					return "", false, err
				}
				if _, err := billing.ParseAmount(number.String(), 18, true); err != nil {
					return "", false, billing.ErrInvalidAmount
				}
				return number.String(), true, nil
			}
			return "", false, billing.ErrInvalidAmount
		}
		var text string
		decoder = json.NewDecoder(strings.NewReader(string(raw)))
		if err := decoder.Decode(&text); err != nil || strings.TrimSpace(text) == "" {
			return "", false, billing.ErrInvalidAmount
		}
		if _, err := billing.ParseAmount(text, 18, true); err != nil {
			return "", false, billing.ErrInvalidAmount
		}
		return strings.TrimSpace(text), true, nil
	}
	return "", false, nil
}
