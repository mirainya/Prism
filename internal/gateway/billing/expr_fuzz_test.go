package billing

import "testing"

// FuzzParseExpression asserts the parser is total: any input either yields a
// usable expression or an error, and never panics or hangs. A parser reached by
// operator-supplied text has to hold that property before it can gate money.
func FuzzParseExpression(f *testing.F) {
	for _, seed := range []string{
		"p * 3 / 1000000 + c * 15 / 1000000",
		"seconds * tier('res', seconds)",
		"in(resolution, '1080p', '4k') ? 2 : 1",
		"param('p') + 1",
		"((((p))))",
		"p >= 4 && p <= 30",
		"",
		"'",
		"?:",
		"tier(",
	} {
		f.Add(seed)
	}
	declared := declare("p", "c", "seconds", "resolution")
	f.Fuzz(func(t *testing.T, source string) {
		expression, err := ParseExpression(source, declared)
		if err != nil {
			if expression != nil {
				t.Fatalf("error returned alongside an expression: %q", source)
			}
			return
		}
		// A parsed expression must only reference declared identifiers.
		for _, name := range expression.Identifiers() {
			if !declared[name] {
				t.Fatalf("undeclared identifier %q survived parsing of %q", name, source)
			}
		}
		env := ExprEnv{
			Declared: declared,
			Vars: map[string]ExprValue{
				"p":          ExprNumber(mustAmount(t, "1000")),
				"c":          ExprNumber(mustAmount(t, "500")),
				"seconds":    ExprNumber(mustAmount(t, "10")),
				"resolution": ExprString("1080p"),
			},
			Tiers: map[string][]ExprTier{"res": {{UpTo: "", Value: "0.06"}}},
		}
		// Evaluation may legitimately fail; it must not panic.
		if amount, err := expression.Evaluate(env); err == nil && amount.Sign() < 0 {
			t.Fatalf("negative amount from %q", source)
		}
	})
}
