package billing

import (
	"github.com/shopspring/decimal"
)

// evaluator carries the per-evaluation step budget. The budget is deterministic
// on purpose: a wall-clock deadline would let the same expression settle to
// different amounts on a loaded machine, which is not acceptable for money.
type evaluator struct {
	env   ExprEnv
	steps int
}

type evalResult struct {
	kind exprKind
	num  decimal.Decimal
	str  string
	flag bool
}

func (r evalResult) truthy() (bool, error) {
	switch r.kind {
	case exprBool:
		return r.flag, nil
	case exprNumber:
		return !r.num.IsZero(), nil
	default:
		return false, ErrInvalidExpression
	}
}

// Evaluate computes the expression against declared variables and returns a
// non-negative amount. A negative result is rejected: a rate component can only
// add to a charge, never subtract from it.
func (e *Expression) Evaluate(env ExprEnv) (Amount, error) {
	if e == nil || e.root == nil {
		return Amount{}, ErrInvalidExpression
	}
	state := &evaluator{env: env, steps: maxExpressionSteps}
	result, err := state.eval(e.root)
	if err != nil {
		return Amount{}, err
	}
	if result.kind != exprNumber {
		return Amount{}, ErrInvalidExpression
	}
	if result.num.IsNegative() {
		return Amount{}, ErrNegativeAmount
	}
	if result.num.Abs().GreaterThan(maxSQLDecimal) {
		return Amount{}, ErrAmountOverflow
	}
	return Amount{value: result.num}, nil
}

func (v *evaluator) eval(node *exprNode) (evalResult, error) {
	v.steps--
	if v.steps <= 0 {
		return evalResult{}, ErrExpressionLimit
	}
	switch node.kind {
	case nodeNumber:
		return evalResult{kind: exprNumber, num: node.value}, nil
	case nodeString:
		return evalResult{kind: exprString, str: node.text}, nil
	case nodeIdent:
		return v.variable(node.text)
	case nodeTernary:
		condition, err := v.eval(node.children[0])
		if err != nil {
			return evalResult{}, err
		}
		truthy, err := condition.truthy()
		if err != nil {
			return evalResult{}, err
		}
		// Only the taken branch is evaluated, so an untaken branch cannot
		// fail on a variable that is irrelevant for this call.
		if truthy {
			return v.eval(node.children[1])
		}
		return v.eval(node.children[2])
	case nodeBinary:
		return v.binary(node)
	case nodeCall:
		return v.call(node)
	default:
		return evalResult{}, ErrInvalidExpression
	}
}

func (v *evaluator) variable(name string) (evalResult, error) {
	if !v.env.Declared[name] {
		return evalResult{}, ErrUndeclaredIdentifier
	}
	value, ok := v.env.Vars[name]
	if !ok {
		return evalResult{}, ErrMissingFact
	}
	return evalResult(value), nil
}

func (v *evaluator) binary(node *exprNode) (evalResult, error) {
	// Short-circuit logical operators before evaluating the right operand.
	if node.operator == "&&" || node.operator == "||" {
		left, err := v.eval(node.children[0])
		if err != nil {
			return evalResult{}, err
		}
		truthy, err := left.truthy()
		if err != nil {
			return evalResult{}, err
		}
		if node.operator == "&&" && !truthy {
			return evalResult{kind: exprBool, flag: false}, nil
		}
		if node.operator == "||" && truthy {
			return evalResult{kind: exprBool, flag: true}, nil
		}
		right, err := v.eval(node.children[1])
		if err != nil {
			return evalResult{}, err
		}
		value, err := right.truthy()
		if err != nil {
			return evalResult{}, err
		}
		return evalResult{kind: exprBool, flag: value}, nil
	}
	left, err := v.eval(node.children[0])
	if err != nil {
		return evalResult{}, err
	}
	right, err := v.eval(node.children[1])
	if err != nil {
		return evalResult{}, err
	}
	switch node.operator {
	case "==", "!=":
		equal, err := sameValue(left, right)
		if err != nil {
			return evalResult{}, err
		}
		return evalResult{kind: exprBool, flag: equal == (node.operator == "==")}, nil
	case "<", "<=", ">", ">=":
		if left.kind != exprNumber || right.kind != exprNumber {
			return evalResult{}, ErrInvalidExpression
		}
		order := left.num.Cmp(right.num)
		flag := node.operator == "<" && order < 0 || node.operator == "<=" && order <= 0 ||
			node.operator == ">" && order > 0 || node.operator == ">=" && order >= 0
		return evalResult{kind: exprBool, flag: flag}, nil
	}
	if left.kind != exprNumber || right.kind != exprNumber {
		return evalResult{}, ErrInvalidExpression
	}
	switch node.operator {
	case "+":
		return v.number(left.num.Add(right.num))
	case "-":
		return v.number(left.num.Sub(right.num))
	case "*":
		return v.number(left.num.Mul(right.num))
	case "/":
		if right.num.IsZero() {
			return evalResult{}, ErrInvalidExpression
		}
		// Division rounds half-even at a fixed scale so the quotient does not
		// depend on a library-wide precision default.
		return v.number(left.num.DivRound(right.num, expressionDivisionScale+1).RoundBank(expressionDivisionScale))
	default:
		return evalResult{}, ErrInvalidExpression
	}
}

// number rejects an intermediate value that already exceeds what the money
// columns can store, instead of waiting for the final result.
func (v *evaluator) number(value decimal.Decimal) (evalResult, error) {
	if value.Abs().GreaterThan(maxSQLDecimal) {
		return evalResult{}, ErrAmountOverflow
	}
	return evalResult{kind: exprNumber, num: value}, nil
}

func sameValue(left, right evalResult) (bool, error) {
	if left.kind != right.kind {
		return false, ErrInvalidExpression
	}
	switch left.kind {
	case exprNumber:
		return left.num.Cmp(right.num) == 0, nil
	case exprString:
		return left.str == right.str, nil
	case exprBool:
		return left.flag == right.flag, nil
	default:
		return false, ErrInvalidExpression
	}
}

func (v *evaluator) call(node *exprNode) (evalResult, error) {
	switch node.operator {
	case "param":
		return v.variable(node.children[0].text)
	case "in":
		subject, err := v.eval(node.children[0])
		if err != nil {
			return evalResult{}, err
		}
		for _, argument := range node.children[1:] {
			candidate, err := v.eval(argument)
			if err != nil {
				return evalResult{}, err
			}
			equal, err := sameValue(subject, candidate)
			if err != nil {
				return evalResult{}, err
			}
			if equal {
				return evalResult{kind: exprBool, flag: true}, nil
			}
		}
		return evalResult{kind: exprBool, flag: false}, nil
	case "tier":
		return v.tier(node)
	default:
		return evalResult{}, ErrInvalidExpression
	}
}

// tier resolves the first step whose inclusive upper bound covers the subject.
// A subject above every bounded step without a trailing unbounded step is an
// error, not a silent fallback to the top tier.
func (v *evaluator) tier(node *exprNode) (evalResult, error) {
	table, ok := v.env.Tiers[node.children[0].text]
	if !ok {
		return evalResult{}, ErrUndeclaredIdentifier
	}
	if err := validateTiers(table); err != nil {
		return evalResult{}, err
	}
	subject, err := v.eval(node.children[1])
	if err != nil {
		return evalResult{}, err
	}
	if subject.kind != exprNumber {
		return evalResult{}, ErrInvalidExpression
	}
	for _, step := range table {
		bound, unbounded, err := step.bound()
		if err != nil {
			return evalResult{}, ErrInvalidExpression
		}
		if unbounded || subject.num.Cmp(bound) <= 0 {
			value, err := ParseAmount(step.Value, 18, true)
			if err != nil {
				return evalResult{}, ErrInvalidExpression
			}
			return evalResult{kind: exprNumber, num: value.value}, nil
		}
	}
	return evalResult{}, ErrMissingFact
}
