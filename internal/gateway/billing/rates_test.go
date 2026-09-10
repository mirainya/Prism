package billing

import (
	"errors"
	"strings"
	"testing"
)

func testSchedule() RateSchedule {
	return RateSchedule{Currency: Currency{Code: "CNY", Version: 1, FractionDigits: 8,
		RoundingMode: "half_even", MaxAmount: "1000000000"}, Components: []RateComponent{{
		ID: 1, Code: "generation", Unit: "request", Source: QuantityOne, Event: ChargeSucceeded,
		UnitPrice: "0.2", QuantityStep: "0", MaxQuantity: "1",
	}}}
}

func TestRequestChargeDoesNotRequireUsage(t *testing.T) {
	s := testSchedule()
	charge, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}})
	if err != nil || charge.Amount.String() != "0.2" {
		t.Fatalf("charge=%+v error=%v", charge, err)
	}
	charge, err = s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: false}})
	if err != nil || charge.Amount.Sign() != 0 {
		t.Fatalf("unsuccessful charge=%+v error=%v", charge, err)
	}
	if _, err := s.Evaluate(Facts{}); !errors.Is(err, ErrMissingFact) {
		t.Fatalf("unknown event must not be free: %v", err)
	}
}

func TestSecondsUseExactMeasuredQuantityAndStep(t *testing.T) {
	s := testSchedule()
	r := &s.Components[0]
	r.Unit, r.Source, r.UnitPrice, r.MaxQuantity = "second", QuantityGeneratedSeconds, "0.17", "120"
	facts := Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}, Quantities: map[QuantitySource]string{QuantityGeneratedSeconds: "5.1"}}
	charge, err := s.Evaluate(facts)
	if err != nil || charge.Amount.String() != "0.867" {
		t.Fatalf("charge=%+v error=%v", charge, err)
	}
	r.QuantityStep = "1"
	charge, err = s.Evaluate(facts)
	if err != nil || charge.Amount.String() != "1.02" || charge.Lines[0].Quantity != "6" {
		t.Fatalf("stepped charge=%+v error=%v", charge, err)
	}
	reserved, err := s.Reserve()
	if err != nil || reserved.Amount.String() != "20.4" {
		t.Fatalf("reservation=%+v error=%v", reserved, err)
	}
	delete(facts.Quantities, QuantityGeneratedSeconds)
	if _, err := s.Evaluate(facts); !errors.Is(err, ErrMissingFact) {
		t.Fatalf("missing seconds must not default to one: %v", err)
	}
}

func TestTokenComponentsHaveExplicitScale(t *testing.T) {
	s := testSchedule()
	s.Components = []RateComponent{
		{ID: 1, Code: "input", Unit: "token", Source: QuantityInputTokens, Event: ChargeSucceeded, UnitPrice: "2", UnitScale: 6, QuantityStep: "0", MaxQuantity: "1000000"},
		{ID: 2, Code: "output", Unit: "token", Source: QuantityOutputTokens, Event: ChargeSucceeded, UnitPrice: "8", UnitScale: 6, QuantityStep: "0", MaxQuantity: "100000"},
	}
	charge, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}, Quantities: map[QuantitySource]string{
		QuantityInputTokens: "12345", QuantityOutputTokens: "1234",
	}})
	if err != nil || charge.Amount.String() != "0.034562" || len(charge.Lines) != 2 {
		t.Fatalf("charge=%+v error=%v", charge, err)
	}
	reserved, err := s.Reserve()
	if err != nil || reserved.Amount.String() != "2.8" {
		t.Fatalf("reservation=%+v error=%v", reserved, err)
	}
	if _, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}}); !errors.Is(err, ErrMissingFact) {
		t.Fatalf("missing usage must stay unknown: %v", err)
	}
}

func TestScheduleRejectsAmbiguousOrIncompleteComponents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RateSchedule)
	}{
		{"unknown_unit", func(s *RateSchedule) { s.Components[0].Unit = "second" }},
		{"unknown_source", func(s *RateSchedule) { s.Components[0].Source = "metadata.seconds" }},
		{"unknown_event", func(s *RateSchedule) { s.Components[0].Event = "http.200" }},
		{"missing_bound", func(s *RateSchedule) { s.Components[0].MaxQuantity = "" }},
		{"negative_price", func(s *RateSchedule) { s.Components[0].UnitPrice = "-1" }},
		{"oversized_price", func(s *RateSchedule) { s.Components[0].UnitPrice = strings.Repeat("9", 21) }},
		{"missing_currency", func(s *RateSchedule) { s.Currency.Code = "" }},
		{"unknown_rounding", func(s *RateSchedule) { s.Currency.RoundingMode = "default" }},
		{"duplicate_component", func(s *RateSchedule) { s.Components = append(s.Components, s.Components[0]) }},
		{"duplicate_meter", func(s *RateSchedule) {
			r := s.Components[0]
			r.Code = "duplicate"
			s.Components = append(s.Components, r)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSchedule()
			tc.change(&s)
			if _, err := s.Reserve(); err == nil {
				t.Fatal("invalid schedule was accepted")
			}
		})
	}
}

func TestCurrencyRoundingOccursOnceAfterComponentSum(t *testing.T) {
	s := testSchedule()
	s.Currency.FractionDigits = 2
	s.Components[0].UnitPrice = "0.005"
	s.Components = append(s.Components, RateComponent{Code: "accepted", Unit: "request", Source: QuantityOne,
		Event: ChargeAccepted, UnitPrice: "0.005", QuantityStep: "0", MaxQuantity: "1"})
	charge, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true, ChargeAccepted: true}})
	if err != nil || charge.Amount.String() != "0.01" {
		t.Fatalf("charge=%+v error=%v", charge, err)
	}
	s.Components = s.Components[:1]
	for mode, expected := range map[string]string{"half_even": "0", "half_up": "0.01", "floor": "0", "ceiling": "0.01"} {
		s.Currency.RoundingMode = mode
		charge, err = s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}})
		if err != nil || charge.Amount.String() != expected {
			t.Fatalf("mode=%s charge=%+v error=%v", mode, charge, err)
		}
	}
}

func TestExceedingBoundKeepsFullCharge(t *testing.T) {
	s := testSchedule()
	r := &s.Components[0]
	r.Unit, r.Source, r.MaxQuantity = "second", QuantityGeneratedSeconds, "5"
	charge, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}, Quantities: map[QuantitySource]string{QuantityGeneratedSeconds: "8"}})
	if err != nil || charge.Amount.String() != "1.6" || !charge.Lines[0].ExceededBound {
		t.Fatalf("charge=%+v error=%v", charge, err)
	}
}

func TestFractionalTokenAndImageQuantitiesAreRejected(t *testing.T) {
	s := testSchedule()
	s.Components[0].Unit, s.Components[0].Source = "image", QuantityGeneratedImages
	for _, quantity := range []string{"0.5", "-1", "1e2", strings.Repeat("9", 1000)} {
		if _, err := s.Evaluate(Facts{Events: map[ChargeEvent]bool{ChargeSucceeded: true}, Quantities: map[QuantitySource]string{QuantityGeneratedImages: quantity}}); err == nil {
			t.Fatalf("accepted invalid quantity %q", quantity)
		}
	}
}
