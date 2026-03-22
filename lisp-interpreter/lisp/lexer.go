package lisp

import (
	"fmt"
	"strings"
)

// TokenType represents the type of a token.
type TokenType int

const (
	TokenLeftParen TokenType = iota
	TokenRightParen
	TokenNumber
	TokenSymbol
	TokenString
	TokenQuote
	TokenEOF
)

// Token represents a lexical token.
type Token struct {
	Type    TokenType
	Value   string
	Line    int
	Column  int
}

// String returns a string representation of the token.
func (t Token) String() string {
	switch t.Type {
	case TokenLeftParen:
		return "("
	case TokenRightParen:
		return ")"
	case TokenNumber:
		return fmt.Sprintf("NUM:%s", t.Value)
	case TokenSymbol:
		return fmt.Sprintf("SYM:%s", t.Value)
	case TokenString:
		return fmt.Sprintf("STR:%q", t.Value)
	case TokenQuote:
		return "'"
	case TokenEOF:
		return "EOF"
	default:
		return fmt.Sprintf("UNKNOWN:%d", t.Type)
	}
}

// Lexer tokenizes input source code.
type Lexer struct {
	input   string
	pos     int
	line    int
	column  int
	tokens  []Token
}

// NewLexer creates a new Lexer for the given input.
func NewLexer(input string) *Lexer {
	return &Lexer{
		input:  input,
		pos:    0,
		line:   1,
		column: 1,
		tokens: nil,
	}
}

// Lex performs lexical analysis and returns all tokens.
func (l *Lexer) Lex() ([]Token, error) {
	l.tokens = nil
	l.pos = 0
	l.line = 1
	l.column = 1

	for l.pos < len(l.input) {
		if err := l.scanToken(); err != nil {
			return nil, err
		}
	}

	l.tokens = append(l.tokens, Token{Type: TokenEOF, Line: l.line, Column: l.column})
	return l.tokens, nil
}

// scanToken scans the next token from the input.
func (l *Lexer) scanToken() error {
	ch := l.peek()

	// Skip whitespace
	if isWhitespace(ch) {
		l.advance()
		return nil
	}

	// Skip comments (semicolon to end of line)
	if ch == ';' {
		l.skipComment()
		return nil
	}

	// Record position for error reporting
	line, col := l.line, l.column

	switch {
	case ch == '(':
		l.tokens = append(l.tokens, Token{Type: TokenLeftParen, Value: "(", Line: line, Column: col})
		l.advance()

	case ch == ')':
		l.tokens = append(l.tokens, Token{Type: TokenRightParen, Value: ")", Line: line, Column: col})
		l.advance()

	case ch == '\'':
		l.tokens = append(l.tokens, Token{Type: TokenQuote, Value: "'", Line: line, Column: col})
		l.advance()

	case ch == '"':
		return l.scanString()

	case isDigitRune(ch):
		return l.scanNumber()

	case ch == '-':
		// Check if this is a negative number
		if l.peekNext() != 0 && isDigitRune(l.peekNext()) {
			return l.scanNumber()
		}
		// Otherwise, it's a symbol (subtraction operator)
		return l.scanSymbol()

	case isLetterRune(ch) || ch == '+' || ch == '*' || ch == '/' || ch == '=' || ch == '<' || ch == '>':
		return l.scanSymbol()

	case ch == 0:
		// End of input handled in Lex()

	default:
		return fmt.Errorf("unexpected character: %q at line %d, column %d", ch, l.line, l.column)
	}

	return nil
}

// peek returns the current character without advancing.
func (l *Lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return rune(l.input[l.pos])
}

// peekNext returns the next character without advancing.
func (l *Lexer) peekNext() rune {
	if l.pos+1 >= len(l.input) {
		return 0
	}
	return rune(l.input[l.pos+1])
}

// advance moves to the next character.
func (l *Lexer) advance() {
	if l.pos < len(l.input) {
		if l.input[l.pos] == '\n' {
			l.line++
			l.column = 1
		} else {
			l.column++
		}
		l.pos++
	}
}

// skipComment skips characters until end of line.
func (l *Lexer) skipComment() {
	for l.pos < len(l.input) && l.input[l.pos] != '\n' {
		l.advance()
	}
}

// scanString scans a quoted string.
func (l *Lexer) scanString() error {
	line, col := l.line, l.column
	l.advance() // consume opening quote

	var sb strings.Builder
	for l.pos < len(l.input) && l.input[l.pos] != '"' {
		ch := l.input[l.pos]

		// Handle escape sequences
		if ch == '\\' && l.pos+1 < len(l.input) {
			l.advance()
			switch l.input[l.pos] {
			case 'n':
				sb.WriteByte('n')
			case 't':
				sb.WriteByte('t')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(l.input[l.pos])
			}
			l.advance()
			continue
		}

		if ch == '\n' {
			return fmt.Errorf("unterminated string at line %d", line)
		}

		sb.WriteByte(ch)
		l.advance()
	}

	if l.pos >= len(l.input) {
		return fmt.Errorf("unterminated string at line %d", line)
	}

	l.advance() // consume closing quote
	l.tokens = append(l.tokens, Token{Type: TokenString, Value: sb.String(), Line: line, Column: col})
	return nil
}

// scanNumber scans a numeric literal.
func (l *Lexer) scanNumber() error {
	line, col := l.line, l.column
	start := l.pos

	// Check for negative number
	if l.pos < len(l.input) && l.input[l.pos] == '-' {
		l.advance()
	}

	// Read digits
	hasDigits := false
	for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
		hasDigits = true
		l.advance()
	}

	if !hasDigits {
		l.pos = start
		return fmt.Errorf("expected digit after '-' at line %d, column %d", line, col)
	}

	value := l.input[start:l.pos]
	l.tokens = append(l.tokens, Token{Type: TokenNumber, Value: value, Line: line, Column: col})
	return nil
}

// scanSymbol scans an identifier or keyword.
func (l *Lexer) scanSymbol() error {
	line, col := l.line, l.column

	// Check for multi-character operators first (<=, >=)
	if l.pos < len(l.input) {
		switch l.input[l.pos] {
		case '<':
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
				l.advance()
				l.advance()
				l.tokens = append(l.tokens, Token{Type: TokenSymbol, Value: "<=", Line: line, Column: col})
				return nil
			}
		case '>':
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
				l.advance()
				l.advance()
				l.tokens = append(l.tokens, Token{Type: TokenSymbol, Value: ">=", Line: line, Column: col})
				return nil
			}
		case '*', '/', '+', '-':
			// Single character operators - emit as separate token
			ch := l.input[l.pos]
			l.advance()
			l.tokens = append(l.tokens, Token{Type: TokenSymbol, Value: string(ch), Line: line, Column: col})
			return nil
		}
	}

	start := l.pos
	for l.pos < len(l.input) && isSymbolChar(rune(l.input[l.pos])) && !isWhitespace(rune(l.input[l.pos])) {
		l.advance()
	}

	value := l.input[start:l.pos]
	l.tokens = append(l.tokens, Token{Type: TokenSymbol, Value: value, Line: line, Column: col})
	return nil
}

// isWhitespace returns true if the rune is whitespace.
func isWhitespace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}

// isDigit returns true if the byte is a decimal digit.
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// isLetter returns true if the byte is a letter.
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isSymbolChar returns true if the rune can be part of a symbol.
func isSymbolChar(ch rune) bool {
	return isLetterRune(ch) || isDigitRune(ch) || ch == '+' || ch == '-' || ch == '*' || ch == '/' ||
		ch == '=' || ch == '<' || ch == '>' || ch == '_' || ch == '?' || ch == '!' ||
		ch == '.' || ch == '@' || ch == '#' || ch == '$' || ch == '%' || ch == '^' ||
		ch == '&' || ch == '~'
}

// isDigitRune returns true if the rune is a decimal digit.
func isDigitRune(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

// isLetterRune returns true if the rune is a letter.
func isLetterRune(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}