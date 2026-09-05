package billing

import (
	"errors"
	"testing"
)

func TestParseAmountRejectsFloatLikeInputs(t *testing.T) {
	for _, input := range []string{"1e-3", "+1", "01.2", "NaN", "Inf", ""} {
		if _, err := ParseAmount(input, 8, true); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("%q: expected invalid amount, got %v", input, err)
		}
	}
	for _, input := range []string{"-0", "-0.0", "-0.000"} {
		if _, err := ParseAmount(input, 8, false); !errors.Is(err, ErrNegativeAmount) {
			t.Errorf("%q: expected negative zero rejection, got %v", input, err)
		}
	}
}

func TestAmountArithmeticAndBankersRounding(t *testing.T) {
	a := mustAmount(t, "1.005")
	b := mustAmount(t, "2.000")
	if got := a.Add(b).Fixed(3); got != "3.005" {
		t.Fatalf("sum=%s", got)
	}
	if got := a.RoundHalfEven(2).Fixed(2); got != "1.00" {
		t.Fatalf("bankers rounding=%s", got)
	}
}

func TestAmountRange(t *testing.T) {
	value := mustAmount(t, "3")
	if err := value.EnsureRange(mustAmount(t, "2")); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestValidateBalancedLedger(t *testing.T) {
	amount := mustAmount(t, "2.50")
	if err := ValidateBalanced([]LedgerEntry{{Code: "user", Direction: Debit, Amount: amount}, {Code: "income", Direction: Credit, Amount: amount}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBalanced([]LedgerEntry{{Code: "user", Direction: Debit, Amount: amount}, {Code: "income", Direction: Credit, Amount: mustAmount(t, "2.49")}}); err == nil {
		t.Fatal("unbalanced ledger was accepted")
	}
}

func mustAmount(t *testing.T, input string) Amount {
	t.Helper()
	value, err := ParseAmount(input, 18, false)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
