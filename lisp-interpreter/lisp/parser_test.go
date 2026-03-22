package lisp

import (
	"testing"
)

func TestParserEmptyInput(t *testing.T) {
	lexer := NewLexer("")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 0 {
		t.Errorf("Parser.Parse() returned %d expressions, want 0", len(exprs))
	}
}

func TestParserNumbers(t *testing.T) {
	tests := []struct {
		input string
		value int64
	}{
		{"42", 42},
		{"0", 0},
		{"-123", -123},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, _ := lexer.Lex()

			parser := NewParser(tokens)
			exprs, err := parser.Parse()

			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
			}

			num, ok := exprs[0].(Integer)
			if !ok {
				t.Fatalf("Parser.Parse() result type = %T, want Integer", exprs[0])
			}

			if num.Value != tt.value {
				t.Errorf("Parser.Parse() result.Value = %d, want %d", num.Value, tt.value)
			}
		})
	}
}

func TestParserSymbols(t *testing.T) {
	tests := []string{"foo", "+", "-", "my-var"}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			lexer := NewLexer(input)
			tokens, _ := lexer.Lex()

			parser := NewParser(tokens)
			exprs, err := parser.Parse()

			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
			}

			sym, ok := exprs[0].(Symbol)
			if !ok {
				t.Fatalf("Parser.Parse() result type = %T, want Symbol", exprs[0])
			}

			if sym.Name != input {
				t.Errorf("Parser.Parse() result.Name = %q, want %q", sym.Name, input)
			}
		})
	}
}

func TestParserStrings(t *testing.T) {
	tests := []struct {
		input string
		value string
	}{
		{`"hello"`, "hello"},
		{`""`, ""},
		{`"hello world"`, "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, _ := lexer.Lex()

			parser := NewParser(tokens)
			exprs, err := parser.Parse()

			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
			}

			str, ok := exprs[0].(String)
			if !ok {
				t.Fatalf("Parser.Parse() result type = %T, want String", exprs[0])
			}

			if str.Value != tt.value {
				t.Errorf("Parser.Parse() result.Value = %q, want %q", str.Value, tt.value)
			}
		})
	}
}

func TestParserEmptyList(t *testing.T) {
	lexer := NewLexer("()")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	if len(list.Items) != 0 {
		t.Errorf("Parser.Parse() result.Items length = %d, want 0", len(list.Items))
	}
}

func TestParserSimpleList(t *testing.T) {
	lexer := NewLexer("(+ 1 2 3)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	if len(list.Items) != 4 {
		t.Fatalf("Parser.Parse() result.Items length = %d, want 4", len(list.Items))
	}

	// Check first element is a symbol
	sym, ok := list.Items[0].(Symbol)
	if !ok || sym.Name != "+" {
		t.Errorf("Parser.Parse() list.Items[0] = %v, want Symbol{+}", list.Items[0])
	}

	// Check the numbers
	for i := 1; i <= 3; i++ {
		num, ok := list.Items[i].(Integer)
		if !ok || num.Value != int64(i) {
			t.Errorf("Parser.Parse() list.Items[%d] = %v, want Integer{%d}", i, list.Items[i], i)
		}
	}
}

func TestParserNestedList(t *testing.T) {
	lexer := NewLexer("(list 1 (2 3) 4)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	if len(list.Items) != 4 {
		t.Fatalf("Parser.Parse() result.Items length = %d, want 4", len(list.Items))
	}

	// Check the nested list
	nested, ok := list.Items[2].(List)
	if !ok {
		t.Fatalf("Parser.Parse() nested type = %T, want List", list.Items[2])
	}

	if len(nested.Items) != 2 {
		t.Errorf("Parser.Parse() nested.Items length = %d, want 2", len(nested.Items))
	}
}

func TestParserMultipleExpressions(t *testing.T) {
	lexer := NewLexer("42 \"hello\" (+ 1 2)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 3 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 3", len(exprs))
	}

	// Check first is number
	num, ok := exprs[0].(Integer)
	if !ok || num.Value != 42 {
		t.Errorf("Parser.Parse() exprs[0] = %v, want Integer{42}", exprs[0])
	}

	// Check second is string
	str, ok := exprs[1].(String)
	if !ok || str.Value != "hello" {
		t.Errorf("Parser.Parse() exprs[1] = %v, want String{hello}", exprs[1])
	}

	// Check third is list
	list, ok := exprs[2].(List)
	if !ok || len(list.Items) != 3 {
		t.Errorf("Parser.Parse() exprs[2] = %v, want List with 3 items", exprs[2])
	}
}

func TestParserQuote(t *testing.T) {
	lexer := NewLexer("'(1 2 3)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	// Quote should be transformed to (quote expr)
	// So we should have a list with "quote" symbol and the quoted list
	if len(list.Items) != 2 {
		t.Fatalf("Parser.Parse() quoted list.Items length = %d, want 2", len(list.Items))
	}

	sym, ok := list.Items[0].(Symbol)
	if !ok || sym.Name != "quote" {
		t.Errorf("Parser.Parse() quoted list.Items[0] = %v, want Symbol{quote}", list.Items[0])
	}

	quotedList, ok := list.Items[1].(List)
	if !ok {
		t.Fatalf("Parser.Parse() quoted list.Items[1] type = %T, want List", list.Items[1])
	}

	if len(quotedList.Items) != 3 {
		t.Errorf("Parser.Parse() quoted inner list.Items length = %d, want 3", len(quotedList.Items))
	}
}

func TestParserQuoteSymbol(t *testing.T) {
	lexer := NewLexer("'foo")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	if len(list.Items) != 2 {
		t.Fatalf("Parser.Parse() quoted list.Items length = %d, want 2", len(list.Items))
	}

	sym, ok := list.Items[0].(Symbol)
	if !ok || sym.Name != "quote" {
		t.Errorf("Parser.Parse() quoted list.Items[0] = %v, want Symbol{quote}", list.Items[0])
	}

	quotedSym, ok := list.Items[1].(Symbol)
	if !ok || quotedSym.Name != "foo" {
		t.Errorf("Parser.Parse() quoted list.Items[1] = %v, want Symbol{foo}", list.Items[1])
	}
}

func TestParserDeeplyNested(t *testing.T) {
	lexer := NewLexer("(list (list (list 1) 2) 3)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	// Just verify it parses without error
	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	if len(list.Items) != 3 {
		t.Fatalf("Parser.Parse() list.Items length = %d, want 3", len(list.Items))
	}
}

func TestParserUnclosedList(t *testing.T) {
	lexer := NewLexer("(+ 1 2")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	_, err := parser.Parse()

	if err == nil {
		t.Error("Parser.Parse() expected error for unclosed list, got nil")
	}
}

func TestParserUnexpectedRightParen(t *testing.T) {
	lexer := NewLexer(")")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	_, err := parser.Parse()

	if err == nil {
		t.Error("Parser.Parse() expected error for unexpected right parenthesis, got nil")
	}
}

func TestParserComplexExpression(t *testing.T) {
	input := `(def factorial [x]
    (if (= x 0)
        1
        (* x (factorial (- x 1)))))`

	lexer := NewLexer(input)
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
	}

	// Verify we got a list
	list, ok := exprs[0].(List)
	if !ok {
		t.Fatalf("Parser.Parse() result type = %T, want List", exprs[0])
	}

	// Should have 4 items: def, factorial, [x], and the body
	if len(list.Items) < 4 {
		t.Fatalf("Parser.Parse() list.Items length = %d, want at least 4", len(list.Items))
	}
}

func TestParserWhitespaceHandling(t *testing.T) {
	tests := []string{
		"(+ 1 2)",
		"  (  +  1  2  )  ",
		"(+1 2)",
		"\n(+ 1 2)\n",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			lexer := NewLexer(input)
			tokens, _ := lexer.Lex()

			parser := NewParser(tokens)
			exprs, err := parser.Parse()

			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("Parser.Parse() returned %d expressions, want 1", len(exprs))
			}

			list, ok := exprs[0].(List)
			if !ok || len(list.Items) != 3 {
				t.Errorf("Parser.Parse() result = %v, want List with 3 items", exprs[0])
			}
		})
	}
}

func TestParserWithComments(t *testing.T) {
	input := "(+ 1 2) ; add two numbers\n(* 3 4)"
	lexer := NewLexer(input)
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)
	exprs, err := parser.Parse()

	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if len(exprs) != 2 {
		t.Fatalf("Parser.Parse() returned %d expressions, want 2", len(exprs))
	}
}

func TestParserPeekAndAdvance(t *testing.T) {
	lexer := NewLexer("1 2 3")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)

	// Test peek
	token := parser.peek()
	if token.Type != TokenNumber || token.Value != "1" {
		t.Errorf("parser.peek() = %v, want TokenNumber with value 1", token)
	}

	// Test advance
	advanced := parser.advance()
	if advanced.Type != TokenNumber || advanced.Value != "1" {
		t.Errorf("parser.advance() = %v, want TokenNumber with value 1", advanced)
	}

	// Peek should now return second token
	token = parser.peek()
	if token.Type != TokenNumber || token.Value != "2" {
		t.Errorf("parser.peek() after advance = %v, want TokenNumber with value 2", token)
	}
}

func TestParserIsAtEnd(t *testing.T) {
	lexer := NewLexer("42")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)

	if parser.isAtEnd() {
		t.Error("parser.isAtEnd() = true, want false before parsing")
	}

	_, _ = parser.Parse()

	if !parser.isAtEnd() {
		t.Error("parser.isAtEnd() = false after parsing, want true")
	}
}

func TestParserCheck(t *testing.T) {
	lexer := NewLexer("(+ 1 2)")
	tokens, _ := lexer.Lex()

	parser := NewParser(tokens)

	if !parser.check(TokenLeftParen) {
		t.Error("parser.check(TokenLeftParen) = false, want true")
	}

	if parser.check(TokenRightParen) {
		t.Error("parser.check(TokenRightParen) = true, want false")
	}
}