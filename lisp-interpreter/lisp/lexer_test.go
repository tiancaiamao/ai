package lisp

import (
	"testing"
)

func TestLexerEmptyInput(t *testing.T) {
	lexer := NewLexer("")
	tokens, err := lexer.Lex()

	if err != nil {
		t.Fatalf("Lexer.Lex() error = %v", err)
	}

	if len(tokens) != 1 {
		t.Errorf("Lexer.Lex() returned %d tokens, want 1 (EOF)", len(tokens))
	}

	if tokens[0].Type != TokenEOF {
		t.Errorf("Lexer.Lex() first token type = %v, want TokenEOF", tokens[0].Type)
	}
}

func TestLexerNumbers(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"42", []string{"42"}},
		{"0", []string{"0"}},
		{"12345", []string{"12345"}},
		{"-42", []string{"-42"}},
		{"-0", []string{"0"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != len(tt.expected) {
				t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), len(tt.expected))
			}

			for i, expected := range tt.expected {
				if tokens[i].Type != TokenNumber {
					t.Errorf("tokens[%d].Type = %v, want TokenNumber", i, tokens[i].Type)
				}
				if tokens[i].Value != expected {
					t.Errorf("tokens[%d].Value = %q, want %q", i, tokens[i].Value, expected)
				}
			}
		})
	}
}

func TestLexerSymbols(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"foo", []string{"foo"}},
		{"+", []string{"+"}},
		{"-", []string{"-"}},
		{"*", []string{"*"}},
		{"/", []string{"/"}},
		{"my-var", []string{"my-var"}},
		{"function?", []string{"function?"}},
		{"foo-bar-baz", []string{"foo-bar-baz"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != len(tt.expected) {
				t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), len(tt.expected))
			}

			for i, expected := range tt.expected {
				if tokens[i].Type != TokenSymbol {
					t.Errorf("tokens[%d].Type = %v, want TokenSymbol", i, tokens[i].Type)
				}
				if tokens[i].Value != expected {
					t.Errorf("tokens[%d].Value = %q, want %q", i, tokens[i].Value, expected)
				}
			}
		})
	}
}

func TestLexerStrings(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{`""`, ""},
		{`"hello world"`, "hello world"},
		{`"hello\nworld"`, "hello\\nworld"},
		{`"escaped \"quote\""`, `escaped \"quote\"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != 1 {
				t.Fatalf("Lexer.Lex() returned %d tokens, want 1", len(tokens))
			}

			if tokens[0].Type != TokenString {
				t.Errorf("tokens[0].Type = %v, want TokenString", tokens[0].Type)
			}
			if tokens[0].Value != tt.expected {
				t.Errorf("tokens[0].Value = %q, want %q", tokens[0].Value, tt.expected)
			}
		})
	}
}

func TestLexerParentheses(t *testing.T) {
	tests := []struct {
		input string
		want  []TokenType
	}{
		{"()", []TokenType{TokenLeftParen, TokenRightParen}},
		{"(()())", []TokenType{TokenLeftParen, TokenLeftParen, TokenRightParen, TokenLeftParen, TokenRightParen, TokenRightParen}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != len(tt.want) {
				t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), len(tt.want))
			}

			for i, expectedType := range tt.want {
				if tokens[i].Type != expectedType {
					t.Errorf("tokens[%d].Type = %v, want %v", i, tokens[i].Type, expectedType)
				}
			}
		})
	}
}

func TestLexerQuote(t *testing.T) {
	lexer := NewLexer("'(1 2 3)")
	tokens, err := lexer.Lex()

	if err != nil {
		t.Fatalf("Lexer.Lex() error = %v", err)
	}

	// Skip EOF token
	tokens = tokens[:len(tokens)-1]

	expected := []TokenType{TokenQuote, TokenLeftParen, TokenNumber, TokenNumber, TokenNumber, TokenRightParen}
	if len(tokens) != len(expected) {
		t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), len(expected))
	}

	for i, expectedType := range expected {
		if tokens[i].Type != expectedType {
			t.Errorf("tokens[%d].Type = %v, want %v", i, tokens[i].Type, expectedType)
		}
	}
}

func TestLexerWhitespace(t *testing.T) {
	tests := []string{
		"1 2 3",
		"1\t2\t3",
		"1  \t 2  \t  3",
		"1\n2\n3",
		"1\r\n2\r\n3",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			lexer := NewLexer(input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != 3 {
				t.Fatalf("Lexer.Lex() returned %d tokens, want 3", len(tokens))
			}

			for i, token := range tokens {
				if token.Type != TokenNumber {
					t.Errorf("tokens[%d].Type = %v, want TokenNumber", i, token.Type)
				}
			}
		})
	}
}

func TestLexerComments(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"42 ; this is a comment\n123", 2},
		{"; comment only\n42", 1},
		{"42;no space\n123", 2},
		{"(1 ; nested\n2)", 3},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != tt.wantCount {
				t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), tt.wantCount)
			}
		})
	}
}

func TestLexerPositionTracking(t *testing.T) {
	input := "(+ 1 2)\n(* 3 4)"
	lexer := NewLexer(input)
	tokens, err := lexer.Lex()

	if err != nil {
		t.Fatalf("Lexer.Lex() error = %v", err)
	}

	// Check line numbers
	tests := []struct {
		index     int
		line      int
		column    int
		tokenType TokenType
	}{
		{0, 1, 1, TokenLeftParen},
		{1, 1, 2, TokenSymbol},
		{2, 1, 4, TokenNumber},
		{3, 1, 6, TokenNumber},
		{4, 1, 7, TokenRightParen},
		{5, 2, 1, TokenLeftParen},
		{6, 2, 2, TokenSymbol},
		{7, 2, 4, TokenNumber},
		{8, 2, 6, TokenNumber},
		{9, 2, 7, TokenRightParen},
	}

	for _, tt := range tests {
		if tokens[tt.index].Line != tt.line {
			t.Errorf("tokens[%d].Line = %d, want %d", tt.index, tokens[tt.index].Line, tt.line)
		}
		if tokens[tt.index].Column != tt.column {
			t.Errorf("tokens[%d].Column = %d, want %d", tt.index, tokens[tt.index].Column, tt.column)
		}
		if tokens[tt.index].Type != tt.tokenType {
			t.Errorf("tokens[%d].Type = %v, want %v", tt.index, tokens[tt.index].Type, tt.tokenType)
		}
	}
}

func TestLexerComplexExpression(t *testing.T) {
	input := "(def factorial [x] (if (= x 0) 1 (* x (factorial (- x 1))))"
	lexer := NewLexer(input)
	tokens, err := lexer.Lex()

	if err != nil {
		t.Fatalf("Lexer.Lex() error = %v", err)
	}

	// Skip EOF token
	tokens = tokens[:len(tokens)-1]

	// Just verify we got tokens without errors
	if len(tokens) < 20 {
		t.Errorf("Lexer.Lex() returned %d tokens, expected at least 20", len(tokens))
	}

	// Verify first few tokens
	if tokens[0].Type != TokenLeftParen {
		t.Errorf("tokens[0].Type = %v, want TokenLeftParen", tokens[0].Type)
	}
	if tokens[1].Type != TokenSymbol || tokens[1].Value != "def" {
		t.Errorf("tokens[1] = %v, want (SYM:\"def\")", tokens[1])
	}
	if tokens[2].Type != TokenSymbol || tokens[2].Value != "factorial" {
		t.Errorf("tokens[2] = %v, want (SYM:\"factorial\")", tokens[2])
	}
}

func TestLexerUnterminatedString(t *testing.T) {
	input := `"unterminated string`
	lexer := NewLexer(input)
	_, err := lexer.Lex()

	if err == nil {
		t.Error("Lexer.Lex() expected error for unterminated string, got nil")
	}

	expectedErr := "unterminated string"
	if err.Error() != expectedErr && len(err.Error()) < len(expectedErr) {
		t.Errorf("Lexer.Lex() error = %q, want it to contain %q", err.Error(), expectedErr)
	}
}

func TestLexerInvalidCharacter(t *testing.T) {
	input := "@invalid"
	lexer := NewLexer(input)
	_, err := lexer.Lex()

	if err == nil {
		t.Error("Lexer.Lex() expected error for invalid character, got nil")
	}
}

func TestLexerComparisonOperators(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"<= <= <=", []string{"<=", "<=", "<="}},
		{">= >= >=", []string{">=", ">=", ">="}},
		{"< > =", []string{"<", ">", "="}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()

			if err != nil {
				t.Fatalf("Lexer.Lex() error = %v", err)
			}

			// Skip EOF token
			tokens = tokens[:len(tokens)-1]

			if len(tokens) != len(tt.expected) {
				t.Fatalf("Lexer.Lex() returned %d tokens, want %d", len(tokens), len(tt.expected))
			}

			for i, expected := range tt.expected {
				if tokens[i].Type != TokenSymbol {
					t.Errorf("tokens[%d].Type = %v, want TokenSymbol", i, tokens[i].Type)
				}
				if tokens[i].Value != expected {
					t.Errorf("tokens[%d].Value = %q, want %q", i, tokens[i].Value, expected)
				}
			}
		})
	}
}

func TestTokenString(t *testing.T) {
	tests := []struct {
		token Token
		want  string
	}{
		{Token{Type: TokenLeftParen}, "("},
		{Token{Type: TokenRightParen}, ")"},
		{Token{Type: TokenNumber, Value: "42"}, "NUM:42"},
		{Token{Type: TokenSymbol, Value: "foo"}, "SYM:foo"},
		{Token{Type: TokenString, Value: "hello"}, `STR:"hello"`},
		{Token{Type: TokenQuote}, "'"},
		{Token{Type: TokenEOF}, "EOF"},
	}

	for _, tt := range tests {
		if got := tt.token.String(); got != tt.want {
			t.Errorf("Token.String() = %q, want %q", got, tt.want)
		}
	}
}