package billing

import (
	"errors"
	"testing"
)

func intDomain(t *testing.T, min, max string) VarDomain {
	t.Helper()
	domain, err := IntDomain(min, max)
	if err != nil {
		t.Fatal(err)
	}
	return domain
}

func enumDomain(t *testing.T, values ...ExprValue) VarDomain {
	t.Helper()
	domain, err := EnumDomain(values...)
	if err != nil {
		t.Fatal(err)
	}
	return domain
}

func numberValues(t *testing.T, values ...string) []ExprValue {
	t.Helper()
	out := make([]ExprValue, 0, len(values))
	for _, value := range values {
		out = append(out, ExprNumber(mustAmount(t, value)))
	}
	return out
}

// The seedance 2.5 pricing_hints example from the manifest schema must prove a
// bound, otherwise the DSL cannot ship for the adapter it was designed for.
func TestProveUpperBoundSeedanceExample(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("seconds", "priority", "has_video_ref"),
		Domains: map[string]VarDomain{
			"seconds":       intDomain(t, "4", "30"),
			"priority":      enumDomain(t, numberValues(t, "1", "2", "3", "4")...),
			"has_video_ref": BoolDomain(),
		},
	}
	expression, err := ParseExpression(
		"seconds * 0.20 * (priority==4 ? 1.5 : 1) * (has_video_ref==1 ? 1.2 : 1)", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	// 30 * 0.20 * 1.5 * 1.2
	if proof.MaxPrice.String() != "10.8" {
		t.Fatalf("max_price=%s, want 10.8", proof.MaxPrice.String())
	}
	if proof.Witness["seconds"] != "30" || proof.Witness["priority"] != "4" || proof.Witness["has_video_ref"] != "1" {
		t.Fatalf("witness=%v", proof.Witness)
	}
}

func TestProveUpperBoundSplitsAtComparisonThreshold(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("seconds"),
		Domains:  map[string]VarDomain{"seconds": intDomain(t, "1", "100")},
	}
	// The price is capped past 10 seconds, so the maximum sits at the threshold
	// and not at either endpoint of the declared interval.
	expression, err := ParseExpression("seconds <= 10 ? seconds * 2 : 5", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	if proof.MaxPrice.String() != "20" {
		t.Fatalf("max_price=%s, want 20", proof.MaxPrice.String())
	}
	if proof.Witness["seconds"] != "10" {
		t.Fatalf("witness=%v, want seconds=10", proof.Witness)
	}
}

func TestProveUpperBoundUsesTierBoundsAsBreakpoints(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("seconds"),
		Domains:  map[string]VarDomain{"seconds": intDomain(t, "1", "60")},
		// The cheaper unbounded step makes the total non-monotone: 5 seconds at
		// 0.10 beats 6 seconds at 0.06.
		Tiers: map[string][]ExprTier{"res": {{UpTo: "5", Value: "0.10"}, {UpTo: "", Value: "0.06"}}},
	}
	expression, err := ParseExpression("seconds * tier('res', seconds)", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	// 60 * 0.06 is still the peak here, but the proof must have considered the
	// tier edge rather than only the interval ends.
	if proof.MaxPrice.String() != "3.6" {
		t.Fatalf("max_price=%s, want 3.6", proof.MaxPrice.String())
	}
	if proof.Corners < 4 {
		t.Fatalf("corners=%d, want the tier edge to add points", proof.Corners)
	}
}

func TestProveUpperBoundRejectsInteriorPeak(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("seconds"),
		Domains:  map[string]VarDomain{"seconds": intDomain(t, "0", "100")},
	}
	// A downward parabola peaks at 50 with no threshold to reveal it. Endpoints
	// would claim 0, so publication must be refused instead of storing a bound
	// the live charge would exceed.
	expression, err := ParseExpression("seconds * (100 - seconds)", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expression.ProveUpperBound(spec); !errors.Is(err, ErrNonMonotonicDomain) {
		t.Fatalf("err=%v, want ErrNonMonotonicDomain", err)
	}
}

func TestProveUpperBoundRequiresDeclaredDomains(t *testing.T) {
	declared := declare("seconds", "mystery")
	expression, err := ParseExpression("seconds * mystery", declared)
	if err != nil {
		t.Fatal(err)
	}
	// Whitelisted but domain-less: legal to reference, impossible to bound.
	spec := ExpressionSpec{Declared: declared, Domains: map[string]VarDomain{"seconds": intDomain(t, "1", "10")}}
	if _, err := expression.ProveUpperBound(spec); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("err=%v, want ErrBoundNotEnumerable", err)
	}
}

func TestProveUpperBoundRejectsTooManyCorners(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"}
	declared := declare(names...)
	domains := make(map[string]VarDomain, len(names))
	source := ""
	for _, name := range names {
		domains[name] = BoolDomain()
		if source != "" {
			source += " + "
		}
		source += name
	}
	expression, err := ParseExpression(source, declared)
	if err != nil {
		t.Fatal(err)
	}
	// 2^13 = 8192 corners, past the 4096 publication cap.
	if _, err := expression.ProveUpperBound(ExpressionSpec{Declared: declared, Domains: domains}); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("err=%v, want ErrBoundNotEnumerable", err)
	}
}

func TestProveUpperBoundRejectsCornerThatCannotBeEvaluated(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("seconds"),
		Domains:  map[string]VarDomain{"seconds": intDomain(t, "0", "10")},
		// The table does not cover seconds=10, so a legal input has no price.
		Tiers: map[string][]ExprTier{"res": {{UpTo: "5", Value: "0.10"}}},
	}
	expression, err := ParseExpression("seconds * tier('res', seconds)", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expression.ProveUpperBound(spec); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("err=%v, want ErrBoundNotEnumerable", err)
	}
}

func TestProveUpperBoundHandlesEnumStringsAndConstants(t *testing.T) {
	spec := ExpressionSpec{
		Declared: declare("resolution", "seconds", "rate"),
		Domains: map[string]VarDomain{
			"resolution": enumDomain(t, ExprString("720p"), ExprString("1080p"), ExprString("4k")),
			"seconds":    intDomain(t, "4", "8"),
			"rate":       ConstDomain(ExprNumber(mustAmount(t, "0.5"))),
		},
	}
	expression, err := ParseExpression(
		"seconds * rate * (in(resolution, '4k') ? 4 : 1)", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	// 8 * 0.5 * 4
	if proof.MaxPrice.String() != "16" {
		t.Fatalf("max_price=%s, want 16", proof.MaxPrice.String())
	}
	if proof.Witness["resolution"] != "4k" {
		t.Fatalf("witness=%v", proof.Witness)
	}
}

func TestIntDomainRejectsInvertedAndFractionalBounds(t *testing.T) {
	if _, err := IntDomain("10", "4"); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("inverted err=%v", err)
	}
	if _, err := IntDomain("1.5", "4"); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("fractional err=%v", err)
	}
	if _, err := EnumDomain(); !errors.Is(err, ErrBoundNotEnumerable) {
		t.Fatalf("empty enum err=%v", err)
	}
}
