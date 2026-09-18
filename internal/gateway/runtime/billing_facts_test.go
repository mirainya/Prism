package runtime

import (
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

func TestEnrichExpressionFactsAddsExchangeDuration(t *testing.T) {
	facts := billing.Facts{Expr: billing.ExprEnv{
		Declared: map[string]bool{"duration_ms": true},
		Vars:     map[string]billing.ExprValue{},
	}}
	duration := uint64(37)
	facts = enrichExpressionFacts(facts, &duration)
	expression, err := billing.ParseExpression("duration_ms + 1", facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(facts.Expr)
	if err != nil || amount.String() != "38" {
		t.Fatalf("amount=%s err=%v facts=%+v", amount.String(), err, facts)
	}
}

func TestEnrichExpressionFactsDoesNotInventUndeclaredOrOverwrite(t *testing.T) {
	duration := uint64(37)
	undeclared := billing.Facts{Expr: billing.ExprEnv{Vars: map[string]billing.ExprValue{}}}
	if got := enrichExpressionFacts(undeclared, &duration); len(got.Expr.Vars) != 0 {
		t.Fatalf("undeclared environment was widened: %+v", got.Expr.Vars)
	}

	existing := billing.Facts{Expr: billing.ExprEnv{
		Declared: map[string]bool{"duration_ms": true},
		Vars:     map[string]billing.ExprValue{"duration_ms": billing.ExprNumber(mustRuntimeAmount(t, "9"))},
	}}
	got := enrichExpressionFacts(existing, &duration)
	expression, err := billing.ParseExpression("duration_ms", got.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(got.Expr)
	if err != nil || amount.String() != "9" {
		t.Fatalf("amount=%s err=%v facts=%+v", amount.String(), err, got)
	}
}

func mustRuntimeAmount(t *testing.T, value string) billing.Amount {
	t.Helper()
	amount, err := billing.ParseAmount(value, 18, true)
	if err != nil {
		t.Fatal(err)
	}
	return amount
}
