package lisp

import (
	"testing"
)

func TestParseInteger(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Expr
	}{
		{"positive number", "42", Integer{Value: 42}},
		{"zero", "0", Integer{Value: 0}},
		{"negative number", "-5", Integer{Value: -5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}

			parser := NewParser(tokens)
			exprs, err := parser.Parse()
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("expected 1 expression, got %d", len(exprs))
			}

			if !compareExpr(exprs[0], tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, exprs[0])
			}
		})
	}
}

func TestParseSymbol(t *testing.T) {
	lexer := NewLexer("x")
	tokens, err := lexer.Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}

	parser := NewParser(tokens)
	exprs, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(exprs) != 1 {
		t.Fatalf("expected 1 expression, got %d", len(exprs))
	}

	sym, ok := exprs[0].(Symbol)
	if !ok {
		t.Fatalf("expected Symbol, got %T", exprs[0])
	}

	if sym.Name != "x" {
		t.Errorf("expected 'x', got %q", sym.Name)
	}
}

func TestParseString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple string", `"hello"`, "hello"},
		{"empty string", `""`, ""},
		{"with spaces", `"hello world"`, "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}

			parser := NewParser(tokens)
			exprs, err := parser.Parse()
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("expected 1 expression, got %d", len(exprs))
			}

			str, ok := exprs[0].(String)
			if !ok {
				t.Fatalf("expected String, got %T", exprs[0])
			}

			if str.Value != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, str.Value)
			}
		})
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected List
	}{
		{"empty list", "()", List{Items: []Expr{}}},
		{"single element", "(x)", List{Items: []Expr{Symbol{Name: "x"}}}},
		{"multiple elements", "(+ 1 2)", List{Items: []Expr{Symbol{Name: "+"}, Integer{Value: 1}, Integer{Value: 2}}}},
		{"nested list", "(def x (1 2))", List{Items: []Expr{
			Symbol{Name: "def"},
			Symbol{Name: "x"},
			List{Items: []Expr{Integer{Value: 1}, Integer{Value: 2}}},
		}}},
		{"deeply nested", "((1))", List{Items: []Expr{
			List{Items: []Expr{Integer{Value: 1}}},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}

			parser := NewParser(tokens)
			exprs, err := parser.Parse()
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("expected 1 expression, got %d", len(exprs))
			}

			if !compareExpr(exprs[0], tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, exprs[0])
			}
		})
	}
}

func TestParseMultipleExprs(t *testing.T) {
	lexer := NewLexer("1 2 3")
	tokens, err := lexer.Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}

	parser := NewParser(tokens)
	exprs, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(exprs) != 3 {
		t.Fatalf("expected 3 expressions, got %d", len(exprs))
	}

	for i, expected := range []int64{1, 2, 3} {
		integer, ok := exprs[i].(Integer)
		if !ok {
			t.Fatalf("expected Integer at position %d, got %T", i, exprs[i])
		}
		if integer.Value != expected {
			t.Errorf("expected %d at position %d, got %d", expected, i, integer.Value)
		}
	}
}

func TestParseQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"quoted symbol", "'x"},
		{"quoted list", "'(1 2 3)"},
		{"quoted nested", "'((a b) c)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}

			parser := NewParser(tokens)
			exprs, err := parser.Parse()
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if len(exprs) != 1 {
				t.Fatalf("expected 1 expression, got %d", len(exprs))
			}

			// Should parse to (quote <expr>)
			list, ok := exprs[0].(List)
			if !ok {
				t.Fatalf("expected List, got %T", exprs[0])
			}

			if len(list.Items) != 2 {
				t.Fatalf("expected 2 items in quote list, got %d", len(list.Items))
			}

			sym, ok := list.Items[0].(Symbol)
			if !ok || sym.Name != "quote" {
				t.Errorf("expected first item to be 'quote' symbol")
			}
		})
	}
}

func TestParseError(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"unclosed list", "(1 2"},
		{"extra closing paren", "(1 2))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer := NewLexer(tt.input)
			tokens, err := lexer.Lex()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}

			parser := NewParser(tokens)
			_, err = parser.Parse()
			if err == nil {
				t.Errorf("expected parse error for input %q", tt.input)
			}
		})
	}
}

// compareExpr compares two expressions for equality.
func compareExpr(a, b Expr) bool {
	switch ta := a.(type) {
	case Integer:
		tb, ok := b.(Integer)
		return ok && ta.Value == tb.Value
	case Symbol:
		tb, ok := b.(Symbol)
		return ok && ta.Name == tb.Name
	case String:
		tb, ok := b.(String)
		return ok && ta.Value == tb.Value
	case List:
		tb, ok := b.(List)
		if !ok || len(ta.Items) != len(tb.Items) {
			return false
		}
		for i := range ta.Items {
			if !compareExpr(ta.Items[i], tb.Items[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}