package runtime

import (
	"strconv"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// enrichExpressionFacts adds execution metadata that is only known by the
// dispatcher after the provider exchange has completed. Adapter facts remain
// authoritative; an adapter-provided value is never overwritten. A missing
// declaration is also left untouched so this helper cannot widen an adapter's
// manifest whitelist at runtime.
func enrichExpressionFacts(facts billing.Facts, durationMS *uint64) billing.Facts {
	if durationMS == nil || !facts.Expr.Declared["duration_ms"] {
		return facts
	}
	if _, exists := facts.Expr.Vars["duration_ms"]; exists {
		return facts
	}
	amount, err := billing.ParseAmount(strconv.FormatUint(*durationMS, 10), 18, true)
	if err != nil {
		return facts
	}
	if facts.Expr.Vars == nil {
		facts.Expr.Vars = make(map[string]billing.ExprValue)
	}
	facts.Expr.Vars["duration_ms"] = billing.ExprNumber(amount)
	return facts
}
