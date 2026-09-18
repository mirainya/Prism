package adapter

// Adapter Manifest: the business-facing declaration of what an adapter accepts,
// what it returns and what it may be priced on. Descriptor stays the
// security-facing declaration (which code is running); this is deliberately a
// parallel structure and replaces nothing.
//
// The manifest is Go data rather than an embedded YAML file. It is compiled into
// the binary and never hot-reloaded, every instance must agree on it byte for
// byte for the upper-bound proof to agree, and the package already expresses the
// descriptor table this way. A YAML file would add a parser and a loading mode
// without making the value editable at runtime.

import (
	"errors"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

var (
	ErrManifestUnknown = errors.New("adapter: no manifest for this adapter and variant")
	ErrManifestInvalid = errors.New("adapter: manifest is inconsistent")
)

// hintKind is the shape of one declared request parameter.
type hintKind string

const (
	// HintEnum is a closed set of string options.
	HintEnum hintKind = "enum"
	// HintInt is an inclusive integer interval.
	HintInt hintKind = "int"
	// HintBool crosses into an expression as 0/1.
	HintBool hintKind = "bool"
	// HintConditionalEnum is an enum whose selected value changes the interval
	// of a dependent parameter, which is how one upstream sells different
	// duration limits per resolution.
	HintConditionalEnum hintKind = "conditional_enum"
)

// ParamHint is the metadata specification of one key that may appear in an
// adapter's Params payload. It drives frontend form rendering, constraint
// checks and the parameter domains the bound proof enumerates.
type ParamHint struct {
	Kind    hintKind
	Options []string
	Min     string
	Max     string
	Default string
	// Allowed false greys the field out and makes any value a hard violation.
	Allowed *bool
	// Required forbids omitting the key.
	Required bool
	// Cases applies to HintConditionalEnum: each option may narrow a dependent
	// parameter's interval.
	Cases []HintCase
}

// HintCase narrows one dependent parameter when the conditional enum takes
// Value.
type HintCase struct {
	Value     string
	Dependent string
	Min       string
	Max       string
}

// varLayer records which of the three declaration layers a variable came from,
// so the console can group the DSL variable reference the way the operator
// thinks about it.
type varLayer string

const (
	LayerCommon     varLayer = "common"
	LayerCapability varLayer = "capability"
	LayerAdapter    varLayer = "adapter"
)

// BillingVar declares one identifier the DSL may reference. Desc is mandatory:
// a variable the operator cannot have explained to them is not usable in a
// price, so the manifest refuses to declare it.
type BillingVar struct {
	Name string
	Desc string
	// From names the ParamHint that supplies this variable's domain, for the
	// common case where a billing variable is a renamed request parameter
	// (seconds comes from duration). Narrowing the hint narrows the proof.
	From string
	// Domain states the domain directly when no hint backs the variable.
	Domain *VarDomainSpec
}

// VarDomainSpec is a manifest-declared value domain. A variable with neither
// From nor Domain is still legal to reference but cannot be bounded, so any
// expression using it fails the publication gate rather than being charged
// without a reservation.
type VarDomainSpec struct {
	Kind    hintKind
	Options []string
	Numbers []string
	Min     string
	Max     string
}

// Variant is one commercial shape of an adapter: a set of upstream models with
// their own parameter domains, material limits, hard constraints and pricing
// guidance. variants[].Code is the value set of gw_skus.variant_code.
type Variant struct {
	Code            string
	Display         string
	Models          []string
	ParamHints      map[string]ParamHint
	RefMedia        map[string]RefMediaLimit
	HardConstraints []HardConstraint
	PricingHints    PricingHints
	// Tiers are the manifest-declared tier tables tier() may look up.
	Tiers map[string][]billing.ExprTier
}

// RefMediaLimit caps the reference material a request may carry.
type RefMediaLimit struct {
	Max          int
	ItemSeconds  [2]int
	TotalSeconds int
}

// HardConstraint is written into the manifest and cannot be relaxed by
// configuration. The change layer rejects a violating value and the console
// greys the field out.
type HardConstraint struct {
	Field  string
	MustBe string
}

// PricingHints is guidance for the console, never an enforced rule.
type PricingHints struct {
	DefaultMode string
	Example     string
}

// Manifest is one adapter's complete declaration.
type Manifest struct {
	Adapter         string
	AdapterVersion  uint32
	Capability      string
	DownstreamPaths []string
	BillingVars     map[varLayer][]BillingVar
	BillingFuncs    []string
	Variants        []Variant
}

const DefaultVariant = "default"
