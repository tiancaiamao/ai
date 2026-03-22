package lisp

import "fmt"

// Parser converts tokens into AST nodes.
type Parser struct {
	tokens  []Token
	pos     int
}

// NewParser creates a new Parser for the given tokens.
func NewParser(tokens []Token) *Parser {
	return &Parser{
		tokens: tokens,
		pos:    0,
	}
}

// Parse parses all tokens and returns a list of expressions.
func (p *Parser) Parse() ([]Expr, error) {
	var exprs []Expr
	for !p.isAtEnd() {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	return exprs, nil
}

// parseExpr parses a single expression (atom, list, or quoted form).
func (p *Parser) parseExpr() (Expr, error) {
	token := p.peek()

	switch token.Type {
	case TokenNumber:
		return p.parseNumber()
	case TokenSymbol:
		return p.parseSymbol()
	case TokenString:
		return p.parseString()
	case TokenLeftParen:
		return p.parseList()
	case TokenQuote:
		return p.parseQuote()
	case TokenRightParen:
		return nil, p.err("unexpected right parenthesis")
	case TokenEOF:
		return nil, p.err("unexpected end of input")
	default:
		return nil, p.err("unexpected token: %s", token)
	}
}

// parseNumber parses an integer literal.
func (p *Parser) parseNumber() (Expr, error) {
	token := p.peek()
	if token.Type != TokenNumber {
		return nil, p.err("expected number, got %s", token)
	}

	var value int64
	_, err := fmt.Sscanf(token.Value, "%d", &value)
	if err != nil {
		return nil, p.err("invalid number: %s", token.Value)
	}

	p.advance()
	return Integer{Value: value}, nil
}

// parseSymbol parses a symbol.
func (p *Parser) parseSymbol() (Expr, error) {
	token := p.peek()
	if token.Type != TokenSymbol {
		return nil, p.err("expected symbol, got %s", token)
	}

	p.advance()
	return Symbol{Name: token.Value}, nil
}

// parseString parses a string literal.
func (p *Parser) parseString() (Expr, error) {
	token := p.peek()
	if token.Type != TokenString {
		return nil, p.err("expected string, got %s", token)
	}

	p.advance()
	return String{Value: token.Value}, nil
}

// parseList parses a parenthesized list expression.
func (p *Parser) parseList() (Expr, error) {
	// Consume opening parenthesis
	lparen := p.peek()
	if lparen.Type != TokenLeftParen {
		return nil, p.err("expected '(', got %s", lparen)
	}
	p.advance()

	var items []Expr

	// Parse elements until we hit closing paren
	for !p.check(TokenRightParen) && !p.isAtEnd() {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		items = append(items, expr)
	}

	// Check for closing parenthesis
	if p.isAtEnd() {
		return nil, p.err("unclosed list: missing ')' at line %d", lparen.Line)
	}

	if !p.check(TokenRightParen) {
		return nil, p.err("expected ')', got %s", p.peek())
	}

	p.advance() // consume closing paren
	return List{Items: items}, nil
}

// parseQuote parses a quoted expression.
func (p *Parser) parseQuote() (Expr, error) {
	quote := p.peek()
	if quote.Type != TokenQuote {
		return nil, p.err("expected quote, got %s", quote)
	}
	p.advance()

	// Parse the quoted expression
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// Quote is represented as (quote <expr>)
	return List{Items: []Expr{
		Symbol{Name: "quote"},
		expr,
	}}, nil
}

// peek returns the current token without advancing.
func (p *Parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	return p.tokens[p.pos]
}

// advance moves to the next token and returns the previous one.
func (p *Parser) advance() Token {
	if !p.isAtEnd() {
		p.pos++
	}
	return p.tokens[p.pos-1]
}

// check returns true if the current token matches the given type.
func (p *Parser) check(tokenType TokenType) bool {
	if p.isAtEnd() {
		return false
	}
	return p.peek().Type == tokenType
}

// isAtEnd returns true if we've reached the end of tokens.
func (p *Parser) isAtEnd() bool {
	return p.pos >= len(p.tokens) || p.peek().Type == TokenEOF
}

// err creates an error with position information.
func (p *Parser) err(format string, args ...interface{}) error {
	token := p.peek()
	if token.Type == TokenEOF {
		if p.pos > 0 {
			token = p.tokens[p.pos-1]
		}
	}
	errMsg := fmt.Sprintf(format, args...)
	return fmt.Errorf("parse error at line %d, column %d: %s", token.Line, token.Column, errMsg)
}

// String represents a string literal.
type String struct {
	Value string
}

// Eval returns the string itself (self-evaluating).
func (s String) Eval(env *Environment) (Expr, error) {
	return s, nil
}

// String returns the string value.
func (s String) String() string {
	return fmt.Sprintf("%q", s.Value)
}
