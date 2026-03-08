package asm

import (
	"fmt"
	"strings"
	"unicode"
)

// Token types
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenLabel
	TokenMnemonic
	TokenDirective
	TokenNumber
	TokenIdentifier
	TokenSymbol // ( ) , X Y # $ + -
	TokenString
)

// Token represents a lexical token
type Token struct {
	Type     TokenType
	Value    string
	Line     int
	Position int
}

func (t Token) String() string {
	switch t.Type {
	case TokenEOF:
		return "EOF"
	case TokenLabel:
		return "LABEL:" + t.Value
	case TokenMnemonic:
		return "MNEMONIC:" + t.Value
	case TokenDirective:
		return "DIRECTIVE:" + t.Value
	case TokenNumber:
		return "NUMBER:" + t.Value
	case TokenIdentifier:
		return "IDENT:" + t.Value
	case TokenSymbol:
		return "SYMBOL:" + t.Value
	case TokenString:
		return "STRING:" + t.Value
	default:
		return "UNKNOWN"
	}
}

// Lexer performs lexical analysis
type Lexer struct {
	source   string
	position int
	line     int
	col      int
	tokens   []Token
}

func NewLexer(source string) *Lexer {
	l := &Lexer{
		source: source,
		line:   1,
	}
	l.tokenize()
	return l
}

func (l *Lexer) tokenize() {
	for l.position < len(l.source) {
		// Skip whitespace
		for l.position < len(l.source) && unicode.IsSpace(rune(l.source[l.position])) {
			if l.source[l.position] == '\n' {
				l.line++
				l.col = 0
			}
			l.position++
		}
		if l.position >= len(l.source) {
			break
		}

		ch := l.source[l.position]

		// Comment
		if ch == ';' {
			l.skipComment()
			continue
		}

		// Label
		if ch == '.' {
			// Directive
			l.position++
			l.col++
			token := l.readIdentifier()
			l.tokens = append(l.tokens, Token{
				Type:     TokenDirective,
				Value:    strings.ToUpper(token),
				Line:     l.line,
				Position: l.col,
			})
			continue
		}

		// Label definition (at end of line)
		if ch == ':' {
			l.position++
			l.col++
			l.tokens = append(l.tokens, Token{
				Type:     TokenSymbol,
				Value:    ":",
				Line:     l.line,
				Position: l.col,
			})
			continue
		}

		// Number literals
		if ch == '$' || ch == '%' || (ch >= '0' && ch <= '9') {
			l.tokens = append(l.tokens, l.readNumber())
			continue
		}

		// Identifier or mnemonic
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			ident := l.readIdentifier()
			upper := strings.ToUpper(ident)
			// Check if it's a mnemonic
			if isMnemonic(upper) {
				l.tokens = append(l.tokens, Token{
					Type:     TokenMnemonic,
					Value:    upper,
					Line:     l.line,
					Position: l.col,
				})
			} else if upper == "=" {
				l.tokens = append(l.tokens, Token{
					Type:     TokenSymbol,
					Value:    "=",
					Line:     l.line,
					Position: l.col,
				})
			} else {
				// Check if followed by '=' for constant definition
				l.tokens = append(l.tokens, Token{
					Type:     TokenIdentifier,
					Value:    ident,
					Line:     l.line,
					Position: l.col,
				})
			}
			continue
		}

		// Symbols
		if strings.ContainsRune("(),+-<>X Y#", rune(ch)) {
			l.tokens = append(l.tokens, Token{
				Type:     TokenSymbol,
				Value:    string(ch),
				Line:     l.line,
				Position: l.col,
			})
			l.position++
			l.col++
			continue
		}

		// String
		if ch == '"' || ch == '\'' {
			l.tokens = append(l.tokens, l.readString())
			continue
		}

		// Unknown character - skip
		l.position++
		l.col++
	}

	// Add EOF token
	l.tokens = append(l.tokens, Token{
		Type:     TokenEOF,
		Value:    "",
		Line:     l.line,
		Position: l.col,
	})
}

func (l *Lexer) skipComment() {
	for l.position < len(l.source) && l.source[l.position] != '\n' {
		l.position++
	}
}

func (l *Lexer) readNumber() Token {
	line := l.line
	col := l.col

	ch := l.source[l.position]
	l.position++
	l.col++

	var value string
	if ch == '$' {
		// Hex number
		for l.position < len(l.source) && isHexDigit(l.source[l.position]) {
			value += string(l.source[l.position])
			l.position++
			l.col++
		}
		if value == "" {
			return Token{Type: TokenNumber, Value: "$", Line: line, Position: col}
		}
		return Token{Type: TokenNumber, Value: "$" + value, Line: line, Position: col}
	} else if ch == '%' {
		// Binary number
		for l.position < len(l.source) && (l.source[l.position] == '0' || l.source[l.position] == '1') {
			value += string(l.source[l.position])
			l.position++
			l.col++
		}
		if value == "" {
			return Token{Type: TokenNumber, Value: "%", Line: line, Position: col}
		}
		return Token{Type: TokenNumber, Value: "%" + value, Line: line, Position: col}
	} else {
		// Decimal number
		value := string(ch)
		for l.position < len(l.source) && (l.source[l.position] >= '0' && l.source[l.position] <= '9') {
			value += string(l.source[l.position])
			l.position++
			l.col++
		}
		return Token{Type: TokenNumber, Value: value, Line: line, Position: col}
	}
}

func (l *Lexer) readIdentifier() string {
	start := l.position
	for l.position < len(l.source) && ((l.source[l.position] >= 'a' && l.source[l.position] <= 'z') || (l.source[l.position] >= 'A' && l.source[l.position] <= 'Z') || (l.source[l.position] >= '0' && l.source[l.position] <= '9') || l.source[l.position] == '_') {
		l.position++
		l.col++
	}
	return l.source[start:l.position]
}

func (l *Lexer) readString() Token {
	line := l.line
	col := l.col
	quote := l.source[l.position]
	l.position++
	l.col++

	var value string
	for l.position < len(l.source) && l.source[l.position] != quote {
		if l.source[l.position] == '\\' && l.position+1 < len(l.source) {
			l.position++
			switch l.source[l.position] {
			case 'n':
				value += "\n"
			case 't':
				value += "\t"
			case '\\':
				value += "\\"
			case '"':
				value += "\""
			default:
				value += string(l.source[l.position])
			}
		} else {
			value += string(l.source[l.position])
		}
		l.position++
		l.col++
	}
	if l.position < len(l.source) {
		l.position++
		l.col++
	}

	return Token{Type: TokenString, Value: value, Line: line, Position: col}
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isMnemonic(s string) bool {
	mnemonics := map[string]bool{
		"ADC": true, "AND": true, "ASL": true,
		"BCC": true, "BCS": true, "BEQ": true, "BIT": true, "BMI": true,
		"BNE": true, "BPL": true, "BRK": true, "BVC": true, "BVS": true,
		"CLC": true, "CLD": true, "CLI": true, "CLV": true, "CMP": true,
		"CPX": true, "CPY": true,
		"DEC": true, "DEX": true, "DEY": true,
		"EOR": true,
		"INC": true, "INX": true, "INY": true,
		"JMP": true, "JSR": true,
		"LDA": true, "LDX": true, "LDY": true, "LSR": true,
		"NOP": true,
		"ORA": true,
		"PHA": true, "PHP": true, "PLA": true, "PLP": true,
		"ROL": true, "ROR": true, "RTI": true, "RTS": true,
		"SBC": true, "SEC": true, "SED": true, "SEI": true,
		"STA": true, "STX": true, "STY": true,
		"TAX": true, "TAY": true, "TSX": true, "TXA": true, "TXS": true, "TYA": true,
	}
	return mnemonics[s]
}

// GetTokens returns the token list
func (l *Lexer) Tokens() []Token {
	return l.tokens
}

// Peek returns the next token without consuming it
func (l *Lexer) Peek() Token {
	if len(l.tokens) > 0 {
		return l.tokens[0]
	}
	return Token{Type: TokenEOF}
}

// Next consumes and returns the next token
func (l *Lexer) Next() Token {
	if len(l.tokens) > 0 {
		tok := l.tokens[0]
		l.tokens = l.tokens[1:]
		return tok
	}
	return Token{Type: TokenEOF}
}

// Expect consumes a token of the expected type and returns it
func (l *Lexer) Expect(expected TokenType) (Token, error) {
	tok := l.Next()
	if tok.Type != expected {
		return tok, fmt.Errorf("expected %v but got %v", expected, tok.Type)
	}
	return tok, nil
}

// Remaining returns remaining tokens as a string
func (l *Lexer) Remaining() string {
	var sb strings.Builder
	for i, tok := range l.tokens {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(tok.Value)
	}
	return sb.String()
}