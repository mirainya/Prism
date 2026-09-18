package billing

// Expression pricing evaluates a restricted arithmetic expression instead of a
// single unit price. It is deliberately not a rule engine: an expression yields
// one amount, never a decision. Charge timing stays with charge_event, quantity
// meaning stays with quantity_source, and selectors never see an expression.
//
// Every value is a fixed-point decimal. No float64 exists anywhere in the
// lexer, the AST or the evaluator.

import (
	"errors"

	"github.com/shopspring/decimal"
)

var (
	ErrInvalidExpression    = errors.New("billing: invalid pricing expression")
	ErrExpressionLimit      = errors.New("billing: pricing expression exceeds limit")
	ErrUndeclaredIdentifier = errors.New("billing: identifier is not declared by the adapter manifest")
)

const (
	maxExpressionLength = 1024
	maxExpressionDepth  = 16
	maxExpressionNodes  = 256
	maxExpressionSteps  = 4096
	// Division rounds at this scale so a quotient is reproducible across
	// instances instead of depending on a division precision default.
	expressionDivisionScale = 18
)

type exprKind uint8

const (
	exprNumber exprKind = iota
	exprString
	exprBool
)

// ExprValue is a declared variable value crossing into an expression. Numbers
// arrive as parsed amounts, never as float64.
type ExprValue struct {
	kind exprKind
	num  decimal.Decimal
	str  string
	flag bool
}

func ExprNumber(amount Amount) ExprValue { return ExprValue{kind: exprNumber, num: amount.value} }

func ExprString(value string) ExprValue { return ExprValue{kind: exprString, str: value} }

func ExprBool(value bool) ExprValue { return ExprValue{kind: exprBool, flag: value} }

// ExprTier is one step of a manifest-declared tier table. UpTo is the inclusive
// upper bound as a canonical decimal string; an empty UpTo is unbounded and may
// only appear as the last step.
type ExprTier struct {
	UpTo  string
	Value string
}

// ExprEnv carries everything an expression is allowed to observe. Declared is
// the manifest whitelist; an identifier outside it is rejected before any
// arithmetic happens.
type ExprEnv struct {
	Declared map[string]bool
	Vars     map[string]ExprValue
	Tiers    map[string][]ExprTier
}

func (t ExprTier) bound() (decimal.Decimal, bool, error) {
	if t.UpTo == "" {
		return decimal.Zero, true, nil
	}
	amount, err := ParseAmount(t.UpTo, 18, true)
	if err != nil {
		return decimal.Zero, false, err
	}
	return amount.value, false, nil
}

// validateTiers rejects unordered or unbounded-in-the-middle tables so a tier
// lookup has exactly one answer.
func validateTiers(tiers []ExprTier) error {
	if len(tiers) == 0 || len(tiers) > 64 {
		return ErrInvalidExpression
	}
	previous := decimal.Zero
	for index, tier := range tiers {
		if _, err := ParseAmount(tier.Value, 18, true); err != nil {
			return ErrInvalidExpression
		}
		bound, unbounded, err := tier.bound()
		if err != nil {
			return ErrInvalidExpression
		}
		if unbounded {
			if index != len(tiers)-1 {
				return ErrInvalidExpression
			}
			continue
		}
		if index > 0 && bound.Cmp(previous) <= 0 {
			return ErrInvalidExpression
		}
		previous = bound
	}
	return nil
}
