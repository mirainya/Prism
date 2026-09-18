package billing

import (
	"errors"
	"testing"
)

func declare(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func numberVars(pairs map[string]string, t *testing.T) map[string]ExprValue {
	t.Helper()
	out := make(map[string]ExprValue, len(pairs))
	for name, value := range pairs {
		amount, err := ParseAmount(value, 18, true)
		if err != nil {
			t.Fatalf("parse %s=%s: %v", name, value, err)
		}
		out[name] = ExprNumber(amount)
	}
	return out
}

func TestExpressionEvaluatesTokenPricing(t *testing.T) {
	declared := declare("p", "c")
	expression, err := ParseExpression("p * 3 / 1000000 + c * 15 / 1000000", declared)
	if err != nil {
		t.Fatal(err)
	}
	env := ExprEnv{Declared: declared, Vars: numberVars(map[string]string{"p": "1000000", "c": "2000"}, t)}
	amount, err := expression.Evaluate(env)
	if err != nil {
		t.Fatal(err)
	}
	if amount.String() != "3.03" {
		t.Fatalf("amount=%s, want 3.03", amount.String())
	}
}

func TestExpressionRejectsUndeclaredIdentifier(t *testing.T) {
	for _, source := range []string{"p * 2", "param('p') * 2"} {
		if _, err := ParseExpression(source, declare("c")); !errors.Is(err, ErrUndeclaredIdentifier) {
			t.Fatalf("source %q err=%v, want ErrUndeclaredIdentifier", source, err)
		}
	}
	if _, err := ParseExpression("p * 2", nil); !errors.Is(err, ErrUndeclaredIdentifier) {
		t.Fatalf("nil whitelist err=%v, want ErrUndeclaredIdentifier", err)
	}
}

func TestExpressionRejectsDisallowedSyntax(t *testing.T) {
	declared := declare("p", "seconds")
	cases := map[string]string{
		"unknown function": "sqrt(p)",
		"assignment":       "p = 2",
		"dynamic tier":     "tier(p, 2)",
		"dynamic param":    "param(p)",
		"chained compare":  "1 < p < 3",
		"trailing garbage": "p * 2 )",
		"empty":            "",
		"bare operator":    "* 2",
		"unclosed string":  "in(p, 'a",
		"non ascii":        "p × 2",
		"in without list":  "in(p)",
		"division by zero": "p / 0",
	}
	for name, source := range cases {
		if _, err := ParseExpression(source, declared); err == nil {
			t.Fatalf("%s: source %q parsed but should be rejected", name, source)
		}
	}
}

func TestExpressionEnforcesNodeAndDepthLimits(t *testing.T) {
	declared := declare("p")
	deep := "p"
	for range maxExpressionDepth + 4 {
		deep = "(" + deep + " + 1)"
	}
	if _, err := ParseExpression(deep, declared); !errors.Is(err, ErrExpressionLimit) {
		t.Fatalf("deep expression err=%v, want ErrExpressionLimit", err)
	}
	wide := "p"
	for range maxExpressionNodes {
		wide += " + p"
	}
	if _, err := ParseExpression(wide, declared); !errors.Is(err, ErrExpressionLimit) {
		t.Fatalf("wide expression err=%v, want ErrExpressionLimit", err)
	}
}
