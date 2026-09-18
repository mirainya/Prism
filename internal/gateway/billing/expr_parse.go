package billing

import (
	"slices"

	"github.com/shopspring/decimal"
)

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenNumber
	tokenString
	tokenIdent
	tokenOperator
	tokenParenOpen
	tokenParenClose
	tokenComma
	tokenQuestion
	tokenColon
)

type token struct {
	kind  tokenKind
	text  string
	value decimal.Decimal
}

var operatorSet = map[string]bool{
	"+": true, "-": true, "*": true, "/": true,
	"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"&&": true, "||": true,
}

// lex converts the source into tokens. Only ASCII digits, letters, underscore,
// dots inside identifiers, and the declared operator set are accepted; anything
// else is rejected rather than skipped.
func lex(source string) ([]token, error) {
	if source == "" {
		return nil, ErrInvalidExpression
	}
	// Source length is a size limit, reported like the depth and node limits
	// rather than as a syntax error.
	if len(source) > maxExpressionLength {
		return nil, ErrExpressionLimit
	}
	var tokens []token
	for index := 0; index < len(source); {
		char := source[index]
		switch {
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			index++
		case char >= '0' && char <= '9':
			start := index
			for index < len(source) && (source[index] >= '0' && source[index] <= '9' || source[index] == '.') {
				index++
			}
			amount, err := ParseAmount(source[start:index], 18, true)
			if err != nil {
				return nil, ErrInvalidExpression
			}
			tokens = append(tokens, token{kind: tokenNumber, text: source[start:index], value: amount.value})
		case char == '\'':
			index++
			start := index
			for index < len(source) && source[index] != '\'' {
				if source[index] < 0x20 || source[index] > 0x7e {
					return nil, ErrInvalidExpression
				}
				index++
			}
			if index >= len(source) {
				return nil, ErrInvalidExpression
			}
			tokens = append(tokens, token{kind: tokenString, text: source[start:index]})
			index++
		case char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z':
			start := index
			for index < len(source) && (source[index] == '_' || source[index] == '.' ||
				source[index] >= 'a' && source[index] <= 'z' ||
				source[index] >= 'A' && source[index] <= 'Z' ||
				source[index] >= '0' && source[index] <= '9') {
				index++
			}
			tokens = append(tokens, token{kind: tokenIdent, text: source[start:index]})
		case char == '(':
			tokens = append(tokens, token{kind: tokenParenOpen, text: "("})
			index++
		case char == ')':
			tokens = append(tokens, token{kind: tokenParenClose, text: ")"})
			index++
		case char == ',':
			tokens = append(tokens, token{kind: tokenComma, text: ","})
			index++
		case char == '?':
			tokens = append(tokens, token{kind: tokenQuestion, text: "?"})
			index++
		case char == ':':
			tokens = append(tokens, token{kind: tokenColon, text: ":"})
			index++
		default:
			if index+1 < len(source) && operatorSet[source[index:index+2]] {
				tokens = append(tokens, token{kind: tokenOperator, text: source[index : index+2]})
				index += 2
				continue
			}
			if operatorSet[string(char)] {
				tokens = append(tokens, token{kind: tokenOperator, text: string(char)})
				index++
				continue
			}
			return nil, ErrInvalidExpression
		}
		if len(tokens) > maxExpressionNodes {
			return nil, ErrExpressionLimit
		}
	}
	if len(tokens) == 0 {
		return nil, ErrInvalidExpression
	}
	return append(tokens, token{kind: tokenEOF}), nil
}

type nodeKind uint8

const (
	nodeNumber nodeKind = iota
	nodeString
	nodeIdent
	nodeBinary
	nodeTernary
	nodeCall
)

type exprNode struct {
	kind     nodeKind
	operator string
	text     string
	value    decimal.Decimal
	children []*exprNode
}

// Expression is a parsed, validated pricing expression. It is immutable and
// safe to cache and share across evaluations.
type Expression struct {
	source string
	root   *exprNode
	idents []string
}

func (e *Expression) Source() string { return e.source }

// Identifiers lists every variable the expression reads, for manifest checks
// and for the publication-time upper-bound proof.
func (e *Expression) Identifiers() []string {
	out := make([]string, len(e.idents))
	copy(out, e.idents)
	return out
}

func (e *Expression) String() string { return e.source }

type parser struct {
	tokens []token
	pos    int
	nodes  int
	idents map[string]bool
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	tok := p.tokens[p.pos]
	if tok.kind != tokenEOF {
		p.pos++
	}
	return tok
}

func (p *parser) node(kind nodeKind) (*exprNode, error) {
	p.nodes++
	if p.nodes > maxExpressionNodes {
		return nil, ErrExpressionLimit
	}
	return &exprNode{kind: kind}, nil
}

// depth counts real nesting — parentheses and call arguments — not precedence
// levels. Descending through the six precedence rules is not nesting, so it must
// not consume the budget the AST depth limit is expressed in.
func (p *parser) parseTernary(depth int) (*exprNode, error) {
	if depth > maxExpressionDepth {
		return nil, ErrExpressionLimit
	}
	condition, err := p.parseLogical(depth)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokenQuestion {
		return condition, nil
	}
	p.next()
	whenTrue, err := p.parseLogical(depth)
	if err != nil {
		return nil, err
	}
	if p.next().kind != tokenColon {
		return nil, ErrInvalidExpression
	}
	whenFalse, err := p.parseTernary(depth)
	if err != nil {
		return nil, err
	}
	node, err := p.node(nodeTernary)
	if err != nil {
		return nil, err
	}
	node.children = []*exprNode{condition, whenTrue, whenFalse}
	return node, nil
}

// parseBinary folds one precedence level left-associatively.
func (p *parser) parseBinary(depth int, operators []string, operand func(int) (*exprNode, error)) (*exprNode, error) {
	if depth > maxExpressionDepth {
		return nil, ErrExpressionLimit
	}
	left, err := operand(depth)
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.kind != tokenOperator {
			return left, nil
		}
		matched := false
		for _, candidate := range operators {
			if tok.text == candidate {
				matched = true
				break
			}
		}
		if !matched {
			return left, nil
		}
		p.next()
		right, err := operand(depth)
		if err != nil {
			return nil, err
		}
		node, err := p.node(nodeBinary)
		if err != nil {
			return nil, err
		}
		node.operator, node.children = tok.text, []*exprNode{left, right}
		left = node
	}
}

func (p *parser) parseLogical(depth int) (*exprNode, error) {
	return p.parseBinary(depth, []string{"&&", "||"}, p.parseComparison)
}

// parseComparison is non-associative: `a < b < c` is rejected rather than
// silently folded.
func (p *parser) parseComparison(depth int) (*exprNode, error) {
	if depth > maxExpressionDepth {
		return nil, ErrExpressionLimit
	}
	left, err := p.parseArithmetic(depth)
	if err != nil {
		return nil, err
	}
	tok := p.peek()
	if tok.kind != tokenOperator {
		return left, nil
	}
	switch tok.text {
	case "==", "!=", "<", "<=", ">", ">=":
	default:
		return left, nil
	}
	p.next()
	right, err := p.parseArithmetic(depth)
	if err != nil {
		return nil, err
	}
	node, err := p.node(nodeBinary)
	if err != nil {
		return nil, err
	}
	node.operator, node.children = tok.text, []*exprNode{left, right}
	return node, nil
}

func (p *parser) parseArithmetic(depth int) (*exprNode, error) {
	return p.parseBinary(depth, []string{"+", "-"}, p.parseTerm)
}

func (p *parser) parseTerm(depth int) (*exprNode, error) {
	return p.parseBinary(depth, []string{"*", "/"}, p.parseFactor)
}

var allowedCalls = map[string]bool{"tier": true, "param": true, "in": true}

func (p *parser) parseFactor(depth int) (*exprNode, error) {
	if depth > maxExpressionDepth {
		return nil, ErrExpressionLimit
	}
	tok := p.next()
	switch tok.kind {
	case tokenNumber:
		node, err := p.node(nodeNumber)
		if err != nil {
			return nil, err
		}
		node.value, node.text = tok.value, tok.text
		return node, nil
	case tokenString:
		node, err := p.node(nodeString)
		if err != nil {
			return nil, err
		}
		node.text = tok.text
		return node, nil
	case tokenParenOpen:
		inner, err := p.parseTernary(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.next().kind != tokenParenClose {
			return nil, ErrInvalidExpression
		}
		return inner, nil
	case tokenIdent:
		if p.peek().kind != tokenParenOpen {
			node, err := p.node(nodeIdent)
			if err != nil {
				return nil, err
			}
			node.text = tok.text
			p.idents[tok.text] = true
			return node, nil
		}
		if !allowedCalls[tok.text] {
			return nil, ErrInvalidExpression
		}
		p.next()
		node, err := p.node(nodeCall)
		if err != nil {
			return nil, err
		}
		node.operator = tok.text
		for {
			argument, err := p.parseTernary(depth + 1)
			if err != nil {
				return nil, err
			}
			node.children = append(node.children, argument)
			if p.peek().kind == tokenComma {
				p.next()
				continue
			}
			break
		}
		if p.next().kind != tokenParenClose {
			return nil, ErrInvalidExpression
		}
		if err := validateCallShape(node); err != nil {
			return nil, err
		}
		return node, nil
	default:
		return nil, ErrInvalidExpression
	}
}

// validateCallShape enforces each function's arity and the argument positions
// that must be literals, so a call cannot be assembled dynamically.
func validateCallShape(node *exprNode) error {
	switch node.operator {
	case "tier":
		// tier('<table>', <expr>) looks up a manifest-declared tier table.
		if len(node.children) != 2 || node.children[0].kind != nodeString {
			return ErrInvalidExpression
		}
	case "param":
		// param('<name>') reads one declared variable by literal name.
		if len(node.children) != 1 || node.children[0].kind != nodeString {
			return ErrInvalidExpression
		}
	case "in":
		// in(<expr>, <literal>...) tests membership against literals only.
		if len(node.children) < 2 {
			return ErrInvalidExpression
		}
		for _, argument := range node.children[1:] {
			if argument.kind != nodeString && argument.kind != nodeNumber {
				return ErrInvalidExpression
			}
		}
	default:
		return ErrInvalidExpression
	}
	return nil
}

// ParseExpression parses and validates an expression against the identifiers a
// manifest declares. Passing a nil or empty whitelist rejects every identifier,
// so a caller cannot accidentally accept an undeclared variable.
func ParseExpression(source string, declared map[string]bool) (*Expression, error) {
	tokens, err := lex(source)
	if err != nil {
		return nil, err
	}
	state := &parser{tokens: tokens, idents: make(map[string]bool)}
	root, err := state.parseTernary(0)
	if err != nil {
		return nil, err
	}
	if state.peek().kind != tokenEOF {
		return nil, ErrInvalidExpression
	}
	if depth(root) > maxExpressionDepth {
		return nil, ErrExpressionLimit
	}
	// A literal zero divisor can never produce a price, so reject it at
	// publication time rather than at charge time.
	if hasLiteralZeroDivisor(root) {
		return nil, ErrInvalidExpression
	}
	// param('x') names a variable just like a bare identifier does; both are
	// checked against the same whitelist.
	collectParamNames(root, state.idents)
	names := make([]string, 0, len(state.idents))
	for name := range state.idents {
		if !declared[name] {
			return nil, ErrUndeclaredIdentifier
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return &Expression{source: source, root: root, idents: names}, nil
}

func depth(node *exprNode) int {
	deepest := 0
	for _, child := range node.children {
		if value := depth(child); value > deepest {
			deepest = value
		}
	}
	return deepest + 1
}

func hasLiteralZeroDivisor(node *exprNode) bool {
	if node.kind == nodeBinary && node.operator == "/" &&
		node.children[1].kind == nodeNumber && node.children[1].value.IsZero() {
		return true
	}
	for _, child := range node.children {
		if hasLiteralZeroDivisor(child) {
			return true
		}
	}
	return false
}

func collectParamNames(node *exprNode, into map[string]bool) {
	if node.kind == nodeCall && node.operator == "param" {
		into[node.children[0].text] = true
	}
	for _, child := range node.children {
		collectParamNames(child, into)
	}
}
