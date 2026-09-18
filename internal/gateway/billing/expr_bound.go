package billing

// Publication-time upper-bound proof for expression pricing.
//
// SPEC §6.1 refuses to publish a prepaid SKU whose maximum price cannot be
// stated: Reserve() has to name a pre-authorization amount before the call
// runs, and an expression has no unit price to multiply by max_quantity. This
// file supplies that amount by enumerating the corners of every variable's
// declared domain and taking the largest result.
//
// Endpoints alone are extremal only where the expression is monotone in the
// variable, so an integer axis also carries the literal thresholds the
// expression compares that variable against — comparison literals and tier
// bounds — which splits the domain wherever the slope can change. Each
// resulting sub-interval is then probed for a hidden interior peak.
//
// The probe is a screen, not a symbolic proof. The hard backstop is
// calculate(): a live amount above the stored bound is refused outright, so a
// peak this screen missed costs one uncollected charge, never an overcharge and
// never more than the reservation the user authorized.

import (
	"errors"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"
)

var (
	ErrBoundNotEnumerable = errors.New("billing: pricing expression has no enumerable upper bound")
	ErrNonMonotonicDomain = errors.New("billing: pricing expression peaks between enumerated domain points")
)

const (
	// A SKU needing more corners than this must be split, so that publication
	// stays a bounded operation instead of an open-ended search.
	maxBoundCorners     = 4096
	maxBoundEvaluations = 16384
	maxBoundEnumValues  = 64
	maxBoundAxisValues  = 32
)

var (
	twoDecimal   = decimal.NewFromInt(2)
	threeDecimal = decimal.NewFromInt(3)
	fourDecimal  = decimal.NewFromInt(4)
)

type domainKind uint8

const (
	domainEnum domainKind = iota
	domainInt
	domainBool
	domainConst
)

// VarDomain is the declared value range of one billing variable, translated
// from a manifest param_hints entry.
type VarDomain struct {
	kind   domainKind
	values []ExprValue
	min    decimal.Decimal
	max    decimal.Decimal
}

// EnumDomain enumerates every declared option.
func EnumDomain(values ...ExprValue) (VarDomain, error) {
	if len(values) == 0 || len(values) > maxBoundEnumValues {
		return VarDomain{}, ErrBoundNotEnumerable
	}
	return VarDomain{kind: domainEnum, values: slices.Clone(values)}, nil
}

// IntDomain declares an inclusive integer interval. Both ends are corners; the
// interior is covered by threshold splitting and probing.
func IntDomain(min, max string) (VarDomain, error) {
	low, err := ParseAmount(min, 0, false)
	if err != nil {
		return VarDomain{}, ErrBoundNotEnumerable
	}
	high, err := ParseAmount(max, 0, false)
	if err != nil {
		return VarDomain{}, ErrBoundNotEnumerable
	}
	if low.value.Cmp(high.value) > 0 {
		return VarDomain{}, ErrBoundNotEnumerable
	}
	return VarDomain{kind: domainInt, min: low.value, max: high.value}, nil
}

// BoolDomain enumerates 0 and 1. Manifest bool variables cross into an
// expression as numbers, which is the contract billing_vars states in prose
// ("是否含音频 0/1") and the seedance example relies on (has_video_ref==1).
func BoolDomain() VarDomain {
	return VarDomain{kind: domainBool, values: []ExprValue{
		{kind: exprNumber, num: decimal.Zero},
		{kind: exprNumber, num: oneDecimal},
	}}
}

// ConstDomain pins a variable to a single value.
func ConstDomain(value ExprValue) VarDomain {
	return VarDomain{kind: domainConst, values: []ExprValue{value}}
}

// ExpressionSpec is everything a manifest contributes to one rate's
// expression: the identifier whitelist from the layered billing_vars, the
// enumerable domains from param_hints, and the tier tables it may look up.
// Declared is deliberately allowed to be wider than Domains — a variable can be
// legal to reference yet have no enumerable domain, and that combination must
// fail at publication rather than at charge time.
type ExpressionSpec struct {
	// Digest identifies this manifest coordinate for the parse cache. Two specs
	// with different whitelists must never share a cache entry, so an empty
	// digest disables caching rather than colliding.
	Digest   string
	Declared map[string]bool
	Domains  map[string]VarDomain
	Tiers    map[string][]ExprTier
}

type boundAxis struct {
	name   string
	values []ExprValue
	// integer marks an axis whose consecutive values can hide an interior peak.
	integer bool
}

// axis turns a domain into the ordered value list the enumeration walks.
func (d VarDomain) axis(name string, breakpoints []decimal.Decimal) (boundAxis, error) {
	if d.kind != domainInt {
		if len(d.values) == 0 {
			return boundAxis{}, ErrBoundNotEnumerable
		}
		return boundAxis{name: name, values: d.values}, nil
	}
	points := []decimal.Decimal{d.min, d.max}
	for _, breakpoint := range breakpoints {
		// A threshold at t changes behaviour between t and t+1, so both sides
		// enter the axis; out-of-range thresholds are simply irrelevant.
		floor := breakpoint.Floor()
		for _, candidate := range []decimal.Decimal{floor, floor.Add(oneDecimal)} {
			if candidate.Cmp(d.min) >= 0 && candidate.Cmp(d.max) <= 0 {
				points = append(points, candidate)
			}
		}
	}
	slices.SortFunc(points, func(a, b decimal.Decimal) int { return a.Cmp(b) })
	points = slices.CompactFunc(points, func(a, b decimal.Decimal) bool { return a.Cmp(b) == 0 })
	if len(points) > maxBoundAxisValues {
		return boundAxis{}, fmt.Errorf("%w: %d points on one axis", ErrBoundNotEnumerable, len(points))
	}
	values := make([]ExprValue, 0, len(points))
	for _, point := range points {
		values = append(values, ExprValue{kind: exprNumber, num: point})
	}
	return boundAxis{name: name, values: values, integer: true}, nil
}

// breakpoints records the literal thresholds the expression compares each
// variable against. Splitting an integer axis at those thresholds turns a
// piecewise expression into pieces the endpoints can bound.
type breakpoints struct {
	perVar map[string][]decimal.Decimal
	// shared holds thresholds that could not be attributed to one variable, so
	// they apply to every integer axis rather than being dropped.
	shared []decimal.Decimal
}

// identName reports the variable a node reads, for `x < 5` and `param('x') < 5`
// alike. Anything more complex is not attributable to a single axis.
func identName(node *exprNode) (string, bool) {
	switch {
	case node.kind == nodeIdent:
		return node.text, true
	case node.kind == nodeCall && node.operator == "param":
		return node.children[0].text, true
	default:
		return "", false
	}
}

func (b *breakpoints) add(name string, values ...decimal.Decimal) {
	if name == "" {
		b.shared = append(b.shared, values...)
		return
	}
	b.perVar[name] = append(b.perVar[name], values...)
}

func (b *breakpoints) forVar(name string) []decimal.Decimal {
	return append(slices.Clone(b.perVar[name]), b.shared...)
}

// collectBreakpoints walks the AST for every literal a variable is compared
// against, plus every tier bound a variable is looked up in.
func collectBreakpoints(node *exprNode, tiers map[string][]ExprTier, into *breakpoints) {
	switch {
	case node.kind == nodeBinary:
		switch node.operator {
		case "==", "!=", "<", "<=", ">", ">=":
			left, right := node.children[0], node.children[1]
			if name, ok := identName(left); ok && right.kind == nodeNumber {
				into.add(name, right.value)
			} else if name, ok := identName(right); ok && left.kind == nodeNumber {
				into.add(name, left.value)
			} else {
				// A comparison between compound terms still marks a slope
				// change somewhere, so its literals apply to every axis.
				for _, side := range node.children {
					if side.kind == nodeNumber {
						into.add("", side.value)
					}
				}
			}
		}
	case node.kind == nodeCall && node.operator == "in":
		name, attributed := identName(node.children[0])
		for _, argument := range node.children[1:] {
			if argument.kind == nodeNumber {
				if attributed {
					into.add(name, argument.value)
				} else {
					into.add("", argument.value)
				}
			}
		}
	case node.kind == nodeCall && node.operator == "tier":
		name, attributed := identName(node.children[1])
		for _, step := range tiers[node.children[0].text] {
			bound, unbounded, err := step.bound()
			if err != nil || unbounded {
				continue
			}
			if attributed {
				into.add(name, bound)
			} else {
				into.add("", bound)
			}
		}
	}
	for _, child := range node.children {
		collectBreakpoints(child, tiers, into)
	}
}

// BoundProof is the publication-time evidence behind a stored max_price.
type BoundProof struct {
	// MaxPrice is the largest amount found; it is what gets stored on the rate.
	MaxPrice Amount
	// Corners is how many domain corners were evaluated, and Witness is the
	// assignment that produced MaxPrice. Both exist so the operator can see why
	// a reservation is as large as it is.
	Corners int
	Witness map[string]string
}

// ProveUpperBound enumerates the corners of the declared domains and returns
// the largest amount the expression can produce. Every identifier the
// expression reads must have an enumerable domain; a variable that is merely
// whitelisted is not enough, because a bound cannot be computed from it.
func (e *Expression) ProveUpperBound(spec ExpressionSpec) (BoundProof, error) {
	if e == nil || e.root == nil {
		return BoundProof{}, ErrInvalidExpression
	}
	marks := &breakpoints{perVar: make(map[string][]decimal.Decimal)}
	collectBreakpoints(e.root, spec.Tiers, marks)
	axes := make([]boundAxis, 0, len(e.idents))
	corners := 1
	for _, name := range e.idents {
		domain, ok := spec.Domains[name]
		if !ok {
			return BoundProof{}, fmt.Errorf("%w: variable %q has no declared domain", ErrBoundNotEnumerable, name)
		}
		axis, err := domain.axis(name, marks.forVar(name))
		if err != nil {
			return BoundProof{}, fmt.Errorf("%w: variable %q", err, name)
		}
		corners *= len(axis.values)
		if corners > maxBoundCorners {
			return BoundProof{}, fmt.Errorf("%w: more than %d corners; split the SKU", ErrBoundNotEnumerable, maxBoundCorners)
		}
		axes = append(axes, axis)
	}
	prover := &boundProver{
		expression: e,
		env:        ExprEnv{Declared: spec.Declared, Vars: make(map[string]ExprValue, len(axes)), Tiers: spec.Tiers},
		axes:       axes,
		cursor:     make([]int, len(axes)),
		budget:     maxBoundEvaluations,
		best:       Amount{value: decimal.Zero},
	}
	if err := prover.walk(0); err != nil {
		return BoundProof{}, err
	}
	if !prover.found {
		return BoundProof{}, ErrBoundNotEnumerable
	}
	if prover.best.value.GreaterThan(maxSQLDecimal) {
		return BoundProof{}, ErrAmountOverflow
	}
	return BoundProof{MaxPrice: prover.best, Corners: prover.evaluated, Witness: prover.witness}, nil
}

type boundProver struct {
	expression *Expression
	env        ExprEnv
	axes       []boundAxis
	cursor     []int
	budget     int
	evaluated  int
	found      bool
	best       Amount
	witness    map[string]string
}

func (p *boundProver) walk(index int) error {
	if index == len(p.axes) {
		return p.probe()
	}
	axis := p.axes[index]
	for position, value := range axis.values {
		p.env.Vars[axis.name] = value
		p.cursor[index] = position
		if err := p.walk(index + 1); err != nil {
			return err
		}
	}
	return nil
}

// at evaluates the current assignment. A corner that cannot be evaluated is a
// publication failure, not a skipped corner: the same input would fail at charge
// time, and a price that is undefined on a legal input has no upper bound.
func (p *boundProver) at() (Amount, error) {
	if p.budget <= 0 {
		return Amount{}, fmt.Errorf("%w: evaluation budget exhausted", ErrBoundNotEnumerable)
	}
	p.budget--
	p.evaluated++
	amount, err := p.expression.Evaluate(p.env)
	if err != nil {
		return Amount{}, fmt.Errorf("%w: %w at %s", ErrBoundNotEnumerable, err, formatAssignment(p.env.Vars))
	}
	return amount, nil
}

func (p *boundProver) record(amount Amount) {
	if p.found && amount.Cmp(p.best) <= 0 {
		return
	}
	p.found, p.best = true, amount
	p.witness = formatWitness(p.env.Vars)
}

func (p *boundProver) probe() error {
	amount, err := p.at()
	if err != nil {
		return err
	}
	p.record(amount)
	// Probing an axis does not depend on that axis's current value, so each axis
	// is probed once per assignment of the others — when it sits at its first
	// value — instead of once per corner.
	for index, axis := range p.axes {
		if !axis.integer || p.cursor[index] != 0 {
			continue
		}
		if err := p.probeAxis(index); err != nil {
			return err
		}
	}
	return nil
}

// probeAxis screens each sub-interval of one integer axis for a peak the
// endpoints miss. Thresholds already split the axis wherever a comparison or a
// tier changes behaviour, so an interior value that beats both endpoints of a
// sub-interval means the expression is genuinely non-monotone there and no
// finite bound can be claimed from the endpoints.
func (p *boundProver) probeAxis(index int) error {
	axis := p.axes[index]
	restore := p.env.Vars[axis.name]
	defer func() { p.env.Vars[axis.name] = restore }()
	for position := 0; position+1 < len(axis.values); position++ {
		low, high := axis.values[position].num, axis.values[position+1].num
		interior := interiorPoints(low, high)
		if len(interior) == 0 {
			continue
		}
		p.env.Vars[axis.name] = ExprValue{kind: exprNumber, num: low}
		lowAmount, err := p.at()
		if err != nil {
			return err
		}
		p.env.Vars[axis.name] = ExprValue{kind: exprNumber, num: high}
		highAmount, err := p.at()
		if err != nil {
			return err
		}
		edge := lowAmount
		if highAmount.Cmp(edge) > 0 {
			edge = highAmount
		}
		for _, point := range interior {
			p.env.Vars[axis.name] = ExprValue{kind: exprNumber, num: point}
			amount, err := p.at()
			if err != nil {
				return err
			}
			if amount.Cmp(edge) > 0 {
				return fmt.Errorf("%w: %s=%s yields %s above both ends of [%s,%s]",
					ErrNonMonotonicDomain, axis.name, point.String(), amount.String(), low.String(), high.String())
			}
		}
	}
	return nil
}

// interiorPoints samples the inside of an integer interval. Three samples catch
// the shapes a price expression realistically takes (a single peak or trough)
// without turning publication into an exhaustive sweep.
func interiorPoints(low, high decimal.Decimal) []decimal.Decimal {
	span := high.Sub(low)
	if span.Cmp(twoDecimal) < 0 {
		return nil
	}
	candidates := []decimal.Decimal{
		low.Add(span.Div(fourDecimal).Floor()),
		low.Add(span.Div(twoDecimal).Floor()),
		low.Add(span.Mul(threeDecimal).Div(fourDecimal).Floor()),
	}
	points := make([]decimal.Decimal, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Cmp(low) <= 0 || candidate.Cmp(high) >= 0 {
			continue
		}
		if slices.ContainsFunc(points, func(existing decimal.Decimal) bool { return existing.Cmp(candidate) == 0 }) {
			continue
		}
		points = append(points, candidate)
	}
	return points
}

func renderExprValue(value ExprValue) string {
	switch value.kind {
	case exprNumber:
		return value.num.String()
	case exprString:
		return value.str
	default:
		if value.flag {
			return "true"
		}
		return "false"
	}
}

func formatWitness(vars map[string]ExprValue) map[string]string {
	witness := make(map[string]string, len(vars))
	for name, value := range vars {
		witness[name] = renderExprValue(value)
	}
	return witness
}

func formatAssignment(vars map[string]ExprValue) string {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	slices.Sort(names)
	rendered := ""
	for _, name := range names {
		if rendered != "" {
			rendered += ", "
		}
		rendered += name + "=" + renderExprValue(vars[name])
	}
	return "{" + rendered + "}"
}
