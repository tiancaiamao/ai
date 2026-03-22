package lisp

import (
	"reflect"
	"testing"
)

func TestLexer_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Token
	}{
		{
			name:  "empty input",
			input: "",
			expected: []Token{
				{Type: TokenEOF, Value: "", Line: 1, Column: 1},
			},
		},
		{
			name:  "whitespace only",
			input: "   \t\n  ",
			expected: []Token{
				{Type: TokenEOF, Value: "", Line: 2, Column: 3},
			},
		},
		{
			name:  "left paren",
			input: "(",
			expected: []Token{
				{Type: TokenLeftParen, Value: "(", Line: 1, Column: 1},
				{Type: TokenEOF, Value: "", Line: 1, Column: 2},
			},
		},
		{
			name:  "right paren",
			input: ")",
			expected: []Token{
				{Type: TokenRightParen, Value: ")", Line: 1, Column: 1},
				{Type: TokenEOF, Value: "", Line: 1, Column: 2},
			},
		},
		{
			name:  "simple expression",
			input: "(+ 1 2)",
			expected: []Token{
				{Type: TokenLeftParen, Value: "(", Line: 1, Column: 1},
				{Type: TokenSymbol, Value: "+", Line: 1, Column: 2},
				{Type: TokenNumber, Value: "1", Line: 1, Column: 4},
				{Type: TokenNumber, Value: "2", Line: 1, Column: 6},
				{Type: TokenRightParen, Value: ")", Line: 1, Column: 7},
				{Type: TokenEOF, Value: "", Line: 1, Column: 8},
			},
		},
		{
			name:  "negative number",
			input: "-42",
			expected: []Token{
				{Type: TokenNumber, Value: "-42", Line: 1, Column: 1},
				{Type: TokenEOF, Value: "", Line: 1, Column: 4},
			},
		},
		{
			name:  "string literal",
			input: `"hello world"`,
			expected: []Token{
				{Type: TokenString, Value: "hello world", Line: 1, Column: 1},
				{Type: TokenEOF, Value: "", Line: 1, Column: 14},
			},
		},
		{
			name:  "quote",
			input: "'x",
			expected: []Token{
				{Type: TokenQuote, Value: "'", Line: 1, Column: 1},
				{Type: TokenSymbol, Value: "x", Line: 1, Column: 2},
				{Type: TokenEOF, Value: "", Line: 1, Column: 3},
			},
		},
		{
			name:  "comment",
			input: "; this is a comment\n(+ 1 2)",
			expected: []Token{
				{Type: TokenLeftParen, Value: "(", Line: 2, Column: 1},
				{Type: TokenSymbol, Value: "+", Line: 2, Column: 2},
				{Type: TokenNumber, Value: "1", Line: 2, Column: 4},
				{Type: TokenNumber, Value: "2", Line: 2, Column: 6},
				{Type: TokenRightParen, Value: ")", Line: 2, Column: 7},
				{Type: TokenEOF, Value: "", Line: 2, Column: 8},
			},
		},
		{
			name:  "symbols with operators",
			input: "def fn lambda if > < = >= <= * / + -",
			expected: []Token{
				{Type: TokenSymbol, Value: "def", Line: 1, Column: 1},
				{Type: TokenSymbol, Value: "fn", Line: 1, Column: 5},
				{Type: TokenSymbol, Value: "lambda", Line: 1, Column: 8},
				{Type: TokenSymbol, Value: "if", Line: 1, Column: 15},
				{Type: TokenSymbol, Value: ">", Line: 1, Column: 18},
				{Type: TokenSymbol, Value: "<", Line: 1, Column: 20},
				{Type: TokenSymbol, Value: "=", Line: 1, Column: 22},
				{Type: TokenSymbol, Value: ">=", Line: 1, Column: 24},
				{Type: TokenSymbol, Value: "<=", Line: 1, Column: 27},
				{Type: TokenSymbol, Value: "*", Line: 1, Column: 30},
				{Type: TokenSymbol, Value: "/", Line: 1, Column: 32},
				{Type: TokenSymbol, Value: "+", Line: 1, Column: 34},
				{Type: TokenSymbol, Value: "-", Line: 1, Column: 36},
				{Type: TokenEOF, Value: "", Line: 1, Column: 37},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(tokens, tt.expected) {
				t.Errorf("tokens mismatch:\ngot:      %v\nwant: %v", tokens, tt.expected)
			}
		})
	}
}

func TestLexer_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unterminated string",
			input: `"hello`,
		},
		{
			name:  "invalid character",
			input: "`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			_, err := lexer.Lex()
			if err == nil {
				t.Errorf("expected error for input %q", tt.input)
			}
		})
	}
}