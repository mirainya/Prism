package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

var manifestLayers = []varLayer{LayerCommon, LayerCapability, LayerAdapter}

// buildExpressionSpec flattens the three declaration layers into the DSL
// whitelist and translates the variant's parameter hints into the domains the
// bound proof enumerates. A variable declared in more than one layer is a
// manifest error, not a silent override: which layer wins would decide the
// domain, and therefore the price.
func buildExpressionSpec(manifest Manifest, variant Variant) (billing.ExpressionSpec, error) {
	declared := make(map[string]bool)
	domains := make(map[string]billing.VarDomain)
	for _, layer := range manifestLayers {
		for _, declaration := range manifest.BillingVars[layer] {
			if declared[declaration.Name] {
				return billing.ExpressionSpec{}, fmt.Errorf("%w: %s declares %q in more than one layer",
					ErrManifestInvalid, manifest.Adapter, declaration.Name)
			}
			declared[declaration.Name] = true
			domain, ok, err := resolveDomain(declaration, variant)
			if err != nil {
				return billing.ExpressionSpec{}, fmt.Errorf("%w: %s variant %s variable %q: %w",
					ErrManifestInvalid, manifest.Adapter, variant.Code, declaration.Name, err)
			}
			if ok {
				domains[declaration.Name] = domain
			}
		}
	}
	return billing.ExpressionSpec{
		Digest:   expressionSpecDigest(manifest, variant),
		Declared: declared,
		Domains:  domains,
		Tiers:    variant.Tiers,
	}, nil
}

// resolveDomain prefers an explicit Domain, then the named ParamHint. The
// missing case is legal and reported as absent: the variable can be referenced,
// and any expression that references it fails the bound proof.
func resolveDomain(declaration BillingVar, variant Variant) (billing.VarDomain, bool, error) {
	if declaration.Domain != nil {
		domain, err := domainFromSpec(*declaration.Domain)
		return domain, err == nil, err
	}
	if declaration.From == "" {
		return billing.VarDomain{}, false, nil
	}
	hint, ok := variant.ParamHints[declaration.From]
	if !ok {
		// A capability-level variable often maps to a parameter only some
		// variants accept (task_mode exists on seedance25 alone). The variable
		// stays referenceable and unbounded here, so an expression that uses it
		// on this variant fails the bound proof instead of being charged
		// without a provable reservation.
		return billing.VarDomain{}, false, nil
	}
	domain, ok, err := domainFromHint(hint, declaration.From, variant)
	return domain, ok, err
}

func domainFromSpec(spec VarDomainSpec) (billing.VarDomain, error) {
	switch spec.Kind {
	case HintBool:
		return billing.BoolDomain(), nil
	case HintInt:
		return billing.IntDomain(spec.Min, spec.Max)
	case HintEnum:
		return enumDomainFrom(spec.Options, spec.Numbers)
	default:
		return billing.VarDomain{}, fmt.Errorf("unsupported domain kind %q", spec.Kind)
	}
}

// enumDomainFrom builds an enum domain from string options, numeric options, or
// both. Numbers stay separate from strings because an expression comparing
// priority==4 needs a number, while in(resolution,'4k') needs a string, and
// silently coercing between them would change what a comparison means.
func enumDomainFrom(options, numbers []string) (billing.VarDomain, error) {
	values := make([]billing.ExprValue, 0, len(options)+len(numbers))
	for _, option := range options {
		values = append(values, billing.ExprString(option))
	}
	for _, number := range numbers {
		amount, err := billing.ParseAmount(number, 18, true)
		if err != nil {
			return billing.VarDomain{}, fmt.Errorf("enum value %q is not a decimal", number)
		}
		values = append(values, billing.ExprNumber(amount))
	}
	return billing.EnumDomain(values...)
}

// domainFromHint translates one param_hint. A conditional_enum contributes the
// union of its cases for the dependent parameter, which is the widest domain the
// variant can produce and therefore the safe one to bound against.
func domainFromHint(hint ParamHint, name string, variant Variant) (billing.VarDomain, bool, error) {
	if hint.Allowed != nil && !*hint.Allowed {
		// A field the variant forbids can never reach the upstream, so it has no
		// domain and any price depending on it must not publish.
		return billing.VarDomain{}, false, nil
	}
	switch hint.Kind {
	case HintBool:
		return billing.BoolDomain(), true, nil
	case HintInt:
		low, high := hint.Min, hint.Max
		// A conditional_enum elsewhere in the variant may widen this interval
		// per case; the union of all cases is what the variant can actually
		// accept.
		caseLow, caseHigh, found := conditionalRange(name, variant)
		if found {
			low, high = widen(low, high, caseLow, caseHigh)
		}
		domain, err := billing.IntDomain(low, high)
		return domain, err == nil, err
	case HintEnum:
		domain, err := enumDomainFrom(hint.Options, nil)
		return domain, err == nil, err
	case HintConditionalEnum:
		options := make([]string, 0, len(hint.Cases))
		for _, item := range hint.Cases {
			if !slices.Contains(options, item.Value) {
				options = append(options, item.Value)
			}
		}
		domain, err := enumDomainFrom(options, nil)
		return domain, err == nil, err
	default:
		return billing.VarDomain{}, false, fmt.Errorf("unsupported hint kind %q", hint.Kind)
	}
}

// conditionalRange returns the union of every case bound placed on name by a
// conditional enum in this variant.
func conditionalRange(name string, variant Variant) (low, high string, found bool) {
	for _, hint := range variant.ParamHints {
		if hint.Kind != HintConditionalEnum {
			continue
		}
		for _, item := range hint.Cases {
			if item.Dependent != name || item.Min == "" || item.Max == "" {
				continue
			}
			if !found {
				low, high, found = item.Min, item.Max, true
				continue
			}
			low, high = widen(low, high, item.Min, item.Max)
		}
	}
	return low, high, found
}

// widen returns the union of two integer intervals given as decimal strings. A
// malformed bound is left to IntDomain to reject.
func widen(low, high, otherLow, otherHigh string) (string, string) {
	if lower(otherLow, low) {
		low = otherLow
	}
	if lower(high, otherHigh) {
		high = otherHigh
	}
	return low, high
}

func lower(left, right string) bool {
	a, errA := billing.ParseAmount(left, 18, false)
	b, errB := billing.ParseAmount(right, 18, false)
	if errA != nil || errB != nil {
		return false
	}
	return a.Cmp(b) < 0
}

// expressionSpecDigest keys the parse cache. It covers the whitelist, the
// domains and the tier tables, so widening a domain in a new binary produces a
// different key instead of reusing an expression validated against the old one.
func expressionSpecDigest(manifest Manifest, variant Variant) string {
	var body strings.Builder
	body.WriteString("prism-adapter-manifest-v1\n")
	body.WriteString(manifest.Adapter)
	body.WriteByte('@')
	body.WriteString(uintString(manifest.AdapterVersion))
	body.WriteByte('/')
	body.WriteString(variant.Code)
	body.WriteByte('\n')
	for _, layer := range manifestLayers {
		for _, declaration := range manifest.BillingVars[layer] {
			fmt.Fprintf(&body, "var\t%s\t%s\t%s\t%s\n", layer, declaration.Name, declaration.From, declaration.Desc)
			if declaration.Domain != nil {
				fmt.Fprintf(&body, "dom\t%s\t%s\t%s\t%s\t%s\n", declaration.Domain.Kind,
					strings.Join(declaration.Domain.Options, ","), strings.Join(declaration.Domain.Numbers, ","),
					declaration.Domain.Min, declaration.Domain.Max)
			}
		}
	}
	for _, name := range slices.Sorted(maps(variant.ParamHints)) {
		hint := variant.ParamHints[name]
		fmt.Fprintf(&body, "hint\t%s\t%s\t%s\t%s\t%s\t%v\n", name, hint.Kind,
			strings.Join(hint.Options, ","), hint.Min, hint.Max, hint.Required)
		for _, item := range hint.Cases {
			fmt.Fprintf(&body, "case\t%s\t%s\t%s\t%s\t%s\n", name, item.Value, item.Dependent, item.Min, item.Max)
		}
	}
	for _, name := range slices.Sorted(maps(variant.Tiers)) {
		for _, step := range variant.Tiers[name] {
			fmt.Fprintf(&body, "tier\t%s\t%s\t%s\n", name, step.UpTo, step.Value)
		}
	}
	digest := sha256.Sum256([]byte(body.String()))
	return hex.EncodeToString(digest[:])
}

// maps yields a map's keys; the standard maps.Keys iterator is used through
// slices.Sorted so digest input order never depends on map iteration.
func maps[V any](values map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range values {
			if !yield(key) {
				return
			}
		}
	}
}
