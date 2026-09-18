package adapter

import (
	"fmt"
	"slices"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// manifestFuncs are the only call names the DSL implements. A manifest that
// advertises another one would show the operator a function the parser rejects.
var manifestFuncs = []string{"tier", "param", "in"}

// validateManifest checks everything that must hold before a manifest can price
// anything. It runs at registry construction so a malformed manifest fails the
// build's tests rather than a publication attempt in production.
func validateManifest(manifest Manifest) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s@%d: %s", ErrManifestInvalid, manifest.Adapter, manifest.AdapterVersion,
			fmt.Sprintf(format, args...))
	}
	if manifest.Adapter == "" {
		return fmt.Errorf("%w: manifest has no adapter code", ErrManifestInvalid)
	}
	if manifest.Capability == "" {
		return fail("no capability")
	}
	if len(manifest.DownstreamPaths) == 0 {
		return fail("declares no downstream path")
	}
	for _, name := range manifest.BillingFuncs {
		if !slices.Contains(manifestFuncs, name) {
			return fail("billing func %q is not implemented by the DSL", name)
		}
	}
	// Variants are checked before the variable layers because a hint reference
	// cannot be judged against an empty variant list.
	if len(manifest.Variants) == 0 {
		return fail("declares no variant")
	}
	if err := validateBillingVars(manifest, fail); err != nil {
		return err
	}
	seenVariants := make(map[string]bool, len(manifest.Variants))
	for _, variant := range manifest.Variants {
		if variant.Code == "" {
			return fail("a variant has no code")
		}
		if seenVariants[variant.Code] {
			return fail("duplicate variant %q", variant.Code)
		}
		seenVariants[variant.Code] = true
		if err := validateVariant(variant, fail); err != nil {
			return err
		}
	}
	return nil
}

// validateBillingVars enforces the layered whitelist's own rules: a name belongs
// to exactly one layer, and every name carries an explanation.
func validateBillingVars(manifest Manifest, fail func(string, ...any) error) error {
	seen := make(map[string]varLayer)
	for _, layer := range manifestLayers {
		for _, declaration := range manifest.BillingVars[layer] {
			if declaration.Name == "" {
				return fail("layer %s declares a variable with no name", layer)
			}
			if previous, exists := seen[declaration.Name]; exists {
				return fail("variable %q is declared in both %s and %s", declaration.Name, previous, layer)
			}
			seen[declaration.Name] = layer
			// A variable the operator cannot have explained to them has no place
			// in a price they are expected to justify.
			if declaration.Desc == "" {
				return fail("variable %q has no description", declaration.Name)
			}
			if declaration.From != "" && declaration.Domain != nil {
				return fail("variable %q takes its domain from both a hint and an inline domain", declaration.Name)
			}
			if declaration.Domain != nil {
				if _, err := domainFromSpec(*declaration.Domain); err != nil {
					return fail("variable %q has an unusable domain: %v", declaration.Name, err)
				}
			}
		}
	}
	for _, layer := range manifestLayers {
		for _, declaration := range manifest.BillingVars[layer] {
			if declaration.From == "" {
				continue
			}
			// From need not resolve on every variant — a parameter one variant
			// accepts and another forbids is normal, and the missing case leaves
			// the variable unbounded there. It must resolve somewhere, though,
			// or the name is a typo that would silently bound nothing.
			if !slices.ContainsFunc(manifest.Variants, func(variant Variant) bool {
				_, ok := variant.ParamHints[declaration.From]
				return ok
			}) {
				return fail("variable %q reads hint %q, which no variant declares", declaration.Name, declaration.From)
			}
		}
	}
	return nil
}

func validateVariant(variant Variant, fail func(string, ...any) error) error {
	if len(variant.Models) == 0 {
		return fail("variant %q lists no upstream model", variant.Code)
	}
	for name, hint := range variant.ParamHints {
		if err := validateHint(name, hint, variant, fail); err != nil {
			return err
		}
	}
	for _, constraint := range variant.HardConstraints {
		if constraint.Field == "" {
			return fail("variant %q has a hard constraint with no field", variant.Code)
		}
	}
	for name, limit := range variant.RefMedia {
		if limit.Max < 0 || limit.TotalSeconds < 0 || limit.ItemSeconds[0] > limit.ItemSeconds[1] {
			return fail("variant %q reference material %q has an inverted limit", variant.Code, name)
		}
	}
	return validateTiers(variant, fail)
}

func validateHint(name string, hint ParamHint, variant Variant, fail func(string, ...any) error) error {
	switch hint.Kind {
	case HintEnum:
		if len(hint.Options) == 0 {
			return fail("variant %q hint %q is an enum with no options", variant.Code, name)
		}
		if hint.Default != "" && !slices.Contains(hint.Options, hint.Default) {
			return fail("variant %q hint %q defaults to %q, which is not an option", variant.Code, name, hint.Default)
		}
	case HintInt:
		if _, err := billing.IntDomain(hint.Min, hint.Max); err != nil {
			return fail("variant %q hint %q has an unusable interval: %v", variant.Code, name, err)
		}
	case HintBool:
		if len(hint.Options) > 0 {
			return fail("variant %q hint %q is a bool with options", variant.Code, name)
		}
	case HintConditionalEnum:
		if len(hint.Cases) == 0 {
			return fail("variant %q hint %q is conditional with no cases", variant.Code, name)
		}
		for _, item := range hint.Cases {
			if item.Value == "" {
				return fail("variant %q hint %q has a case with no value", variant.Code, name)
			}
			if item.Dependent == "" {
				continue
			}
			// A case may only narrow a parameter this variant actually declares,
			// otherwise the narrowing silently applies to nothing.
			if _, ok := variant.ParamHints[item.Dependent]; !ok {
				return fail("variant %q hint %q narrows undeclared parameter %q", variant.Code, name, item.Dependent)
			}
			if _, err := billing.IntDomain(item.Min, item.Max); err != nil {
				return fail("variant %q hint %q case %q has an unusable interval: %v",
					variant.Code, name, item.Value, err)
			}
		}
	default:
		return fail("variant %q hint %q has kind %q", variant.Code, name, hint.Kind)
	}
	return nil
}

// validateTiers checks each tier table is ordered and terminates. The DSL
// rejects a malformed table at evaluation time; catching it here means the
// operator learns at build time instead of at charge time.
func validateTiers(variant Variant, fail func(string, ...any) error) error {
	for name, steps := range variant.Tiers {
		if len(steps) == 0 {
			return fail("variant %q tier table %q is empty", variant.Code, name)
		}
		previous, hasPrevious := billing.Amount{}, false
		for index, step := range steps {
			if step.UpTo == "" {
				if index != len(steps)-1 {
					return fail("variant %q tier table %q has an unbounded step before the last", variant.Code, name)
				}
			} else {
				bound, err := billing.ParseAmount(step.UpTo, 18, true)
				if err != nil {
					return fail("variant %q tier table %q step %d has bound %q: %v", variant.Code, name, index, step.UpTo, err)
				}
				if hasPrevious && bound.Cmp(previous) <= 0 {
					return fail("variant %q tier table %q step %d does not increase", variant.Code, name, index)
				}
				previous, hasPrevious = bound, true
			}
			if _, err := billing.ParseAmount(step.Value, 18, true); err != nil {
				return fail("variant %q tier table %q step %d has value %q: %v", variant.Code, name, index, step.Value, err)
			}
		}
	}
	return nil
}
