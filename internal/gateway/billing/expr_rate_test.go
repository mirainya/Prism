package billing

import (
	"errors"
	"testing"
)

func TestExpressionTierAndMembership(t *testing.T) {
	declared := declare("seconds", "resolution")
	expression, err := ParseExpression(
		"seconds * tier('res', seconds) * (in(resolution, '1080p', '4k') ? 2 : 1)", declared)
	if err != nil {
		t.Fatal(err)
	}
	env := ExprEnv{
		Declared: declared,
		Vars: map[string]ExprValue{
			"seconds":    ExprNumber(mustAmount(t, "10")),
			"resolution": ExprString("1080p"),
		},
		Tiers: map[string][]ExprTier{"res": {
			{UpTo: "5", Value: "0.10"},
			{UpTo: "", Value: "0.06"},
		}},
	}
	amount, err := expression.Evaluate(env)
	if err != nil {
		t.Fatal(err)
	}
	// 10 seconds falls in the unbounded step: 10 * 0.06 * 2.
	if amount.String() != "1.2" {
		t.Fatalf("amount=%s, want 1.2", amount.String())
	}
}

func TestExpressionTierTableMustBeOrdered(t *testing.T) {
	declared := declare("seconds")
	expression, err := ParseExpression("tier('res', seconds)", declared)
	if err != nil {
		t.Fatal(err)
	}
	env := ExprEnv{Declared: declared, Vars: map[string]ExprValue{"seconds": ExprNumber(mustAmount(t, "3"))}}
	for name, table := range map[string][]ExprTier{
		"descending":          {{UpTo: "9", Value: "1"}, {UpTo: "4", Value: "2"}},
		"unbounded in middle": {{UpTo: "", Value: "1"}, {UpTo: "9", Value: "2"}},
		"empty":               {},
		"uncovered subject":   {{UpTo: "1", Value: "1"}},
	} {
		env.Tiers = map[string][]ExprTier{"res": table}
		if _, err := expression.Evaluate(env); err == nil {
			t.Fatalf("%s tier table was accepted", name)
		}
	}
	env.Tiers = map[string][]ExprTier{"other": {{UpTo: "", Value: "1"}}}
	if _, err := expression.Evaluate(env); !errors.Is(err, ErrUndeclaredIdentifier) {
		t.Fatalf("missing tier table err=%v, want ErrUndeclaredIdentifier", err)
	}
}

func TestExpressionRejectsNegativeResultAndMissingVariable(t *testing.T) {
	declared := declare("p")
	expression, err := ParseExpression("p - 100", declared)
	if err != nil {
		t.Fatal(err)
	}
	env := ExprEnv{Declared: declared, Vars: map[string]ExprValue{"p": ExprNumber(mustAmount(t, "1"))}}
	if _, err := expression.Evaluate(env); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative result err=%v, want ErrNegativeAmount", err)
	}
	if _, err := expression.Evaluate(ExprEnv{Declared: declared}); !errors.Is(err, ErrMissingFact) {
		t.Fatalf("missing variable err=%v, want ErrMissingFact", err)
	}
	// An evaluation whitelist narrower than the parse whitelist still refuses.
	if _, err := expression.Evaluate(ExprEnv{Vars: env.Vars}); !errors.Is(err, ErrUndeclaredIdentifier) {
		t.Fatalf("undeclared at eval err=%v, want ErrUndeclaredIdentifier", err)
	}
}

func TestExpressionTernaryOnlyEvaluatesTakenBranch(t *testing.T) {
	declared := declare("has_ref", "base", "surcharge")
	expression, err := ParseExpression("has_ref ? base + surcharge : base", declared)
	if err != nil {
		t.Fatal(err)
	}
	// surcharge is absent, but the false branch never reads it.
	env := ExprEnv{Declared: declared, Vars: map[string]ExprValue{
		"has_ref": ExprBool(false),
		"base":    ExprNumber(mustAmount(t, "0.25")),
	}}
	amount, err := expression.Evaluate(env)
	if err != nil {
		t.Fatal(err)
	}
	if amount.String() != "0.25" {
		t.Fatalf("amount=%s, want 0.25", amount.String())
	}
	env.Vars["has_ref"] = ExprBool(true)
	if _, err := expression.Evaluate(env); !errors.Is(err, ErrMissingFact) {
		t.Fatalf("taken branch err=%v, want ErrMissingFact", err)
	}
}
