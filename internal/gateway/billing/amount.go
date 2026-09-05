// Package billing contains provider-independent money and budget arithmetic.
// Inputs cross this boundary as canonical decimal strings, never float64.
package billing

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

var (
	ErrInvalidAmount  = errors.New("billing: invalid decimal amount")
	ErrNegativeAmount = errors.New("billing: amount cannot be negative")
	ErrAmountOverflow = errors.New("billing: amount exceeds limit")
)

var canonicalDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// Amount is an exact decimal value accepted by the billing boundary.
type Amount struct{ value decimal.Decimal }

func ParseAmount(input string, maxScale int32, nonNegative bool) (Amount, error) {
	if input == "" || maxScale < 0 || maxScale > 18 || !canonicalDecimal.MatchString(input) {
		return Amount{}, ErrInvalidAmount
	}
	value, err := decimal.NewFromString(input)
	if err != nil || value.Exponent() < -maxScale {
		return Amount{}, ErrInvalidAmount
	}
	if input[0] == '-' && value.IsZero() {
		return Amount{}, ErrNegativeAmount
	}
	if nonNegative && value.IsNegative() {
		return Amount{}, ErrNegativeAmount
	}
	return Amount{value: value}, nil
}

func Zero() Amount { return Amount{value: decimal.Zero} }

func (a Amount) String() string { return a.value.String() }

// Scale returns the number of fractional decimal places represented by the
// amount. It is used to enforce the published currency definition at SQL
// boundaries before a DECIMAL value is written.
func (a Amount) Scale() int32 {
	if a.value.Exponent() >= 0 {
		return 0
	}
	return -a.value.Exponent()
}

func (a Amount) Fixed(scale int32) string { return a.value.StringFixed(scale) }

func (a Amount) Sign() int { return a.value.Sign() }

func (a Amount) Cmp(other Amount) int { return a.value.Cmp(other.value) }

func (a Amount) Add(other Amount) Amount { return Amount{value: a.value.Add(other.value)} }

func (a Amount) Sub(other Amount) Amount { return Amount{value: a.value.Sub(other.value)} }

func (a Amount) Mul(other Amount) Amount { return Amount{value: a.value.Mul(other.value)} }

// RoundHalfEven applies the documented default rounding mode at a fixed scale.
func (a Amount) RoundHalfEven(scale int32) Amount { return Amount{value: a.value.RoundBank(scale)} }

func (a Amount) EnsureRange(max Amount) error {
	if a.value.IsNegative() || a.value.GreaterThan(max.value) {
		return fmt.Errorf("%w: %s", ErrAmountOverflow, a.String())
	}
	return nil
}
