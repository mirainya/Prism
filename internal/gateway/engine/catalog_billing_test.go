package engine

import (
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
)

func canonicalPriceFixture() *routing.RouteResult {
	return &routing.RouteResult{Currency: "CNY", CurrencyVersion: 2, SellSchedule: &billing.RateSchedule{
		Currency: billing.Currency{Code: "CNY", Version: 2, FractionDigits: 8, RoundingMode: "half_even", MaxAmount: "1000000"},
		Components: []billing.RateComponent{{Code: "request", Unit: "request", Source: billing.QuantityOne,
			Event: billing.ChargeSucceeded, UnitPrice: "0.3", QuantityStep: "0", MaxQuantity: "1"}},
	}}
}

func TestCanonicalPricingRequiresExplicitSupportedSchedule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*routing.RouteResult)
	}{
		{"no_price", func(r *routing.RouteResult) { r.SellSchedule = nil }},
		{"no_currency", func(r *routing.RouteResult) { r.Currency = "" }},
		{"wrong_version", func(r *routing.RouteResult) { r.CurrencyVersion = 1 }},
		{"seconds", func(r *routing.RouteResult) {
			r.SellSchedule.Components[0].Source = billing.QuantityGeneratedSeconds
			r.SellSchedule.Components[0].Unit = "second"
		}},
		{"delivery", func(r *routing.RouteResult) { r.SellSchedule.Components[0].Event = billing.ChargeDelivered }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := canonicalPriceFixture()
			tc.change(r)
			if _, err := canonicalSellSchedule(r); !errors.Is(err, billing.ErrInvalidRate) {
				t.Fatalf("expected explicit pricing rejection, got %v", err)
			}
		})
	}
}

func TestCanonicalPricingSnapshotAndMissingUsage(t *testing.T) {
	r := canonicalPriceFixture()
	schedule, err := canonicalSellSchedule(r)
	if err != nil {
		t.Fatal(err)
	}
	r.SellSchedule.Components[0].UnitPrice = "9"
	facts, err := canonicalBillingFacts(nil)
	if err != nil {
		t.Fatal(err)
	}
	charge, err := schedule.Evaluate(facts)
	if err != nil || charge.Amount.String() != "0.3" {
		t.Fatalf("charge=%v err=%v", charge, err)
	}
	schedule.Components[0].Unit = "token"
	schedule.Components[0].Source = billing.QuantityInputTokens
	if _, err := schedule.Evaluate(facts); !errors.Is(err, billing.ErrMissingFact) {
		t.Fatalf("missing tokens are not zero: %v", err)
	}
}

func TestCanonicalBillingValidatesCachedTokenSubset(t *testing.T) {
	facts, err := canonicalBillingFacts(&canonical.Usage{InputTokens: 20, CachedInputTokens: 7, OutputTokens: 4})
	if err != nil || facts.Quantities[billing.QuantityUncachedTokens] != "13" || facts.Quantities[billing.QuantityCachedTokens] != "7" {
		t.Fatalf("facts=%v err=%v", facts, err)
	}
	for _, usage := range []*canonical.Usage{{InputTokens: -1}, {OutputTokens: -1}, {InputTokens: 2, CachedInputTokens: 3}, {CachedInputTokens: -1}} {
		if _, err := canonicalBillingFacts(usage); !errors.Is(err, billing.ErrInvalidAmount) {
			t.Fatalf("invalid usage accepted: %v", usage)
		}
	}
}
