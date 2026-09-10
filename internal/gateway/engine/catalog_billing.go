package engine

import (
	"fmt"
	"strconv"

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

func canonicalBillingFacts(usage *canonical.Usage) (billing.Facts, error) {
	facts := billing.Facts{Events: map[billing.ChargeEvent]bool{
		billing.ChargeSucceeded: true, billing.ChargeFailed: false, billing.ChargeCancelled: false,
	}, Quantities: make(map[billing.QuantitySource]string)}
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
	return facts, nil
}
