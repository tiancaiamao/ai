package lisp

import "testing"

func TestNewEnvironment(t *testing.T) {
	env := NewEnvironment()

	if env == nil {
		t.Fatal("NewEnvironment() returned nil")
	}

	if env.data == nil {
		t.Error("NewEnvironment() data map is nil")
	}

	if env.outer != nil {
		t.Error("NewEnvironment() outer environment should be nil")
	}
}

func TestNewEnclosedEnvironment(t *testing.T) {
	outer := NewEnvironment()
	outer.Set("x", Integer{Value: 42})

	inner := NewEnclosedEnvironment(outer)

	if inner == nil {
		t.Fatal("NewEnclosedEnvironment() returned nil")
	}

	if inner.outer != outer {
		t.Error("NewEnclosedEnvironment() outer is not the provided environment")
	}

	if inner.data == nil {
		t.Error("NewEnclosedEnvironment() data map is nil")
	}
}

func TestEnvironmentSetAndGet(t *testing.T) {
	env := NewEnvironment()
	value := Integer{Value: 123}

	env.Set("test-var", value)

	result, err := env.Get("test-var")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}

	resultInt, ok := result.(Integer)
	if !ok {
		t.Fatalf("Environment.Get() result type = %T, want Integer", result)
	}

	if resultInt.Value != 123 {
		t.Errorf("Environment.Get() result.Value = %d, want 123", resultInt.Value)
	}
}

func TestEnvironmentSetOverwrite(t *testing.T) {
	env := NewEnvironment()

	env.Set("x", Integer{Value: 1})
	env.Set("x", Integer{Value: 2})

	result, err := env.Get("x")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}

	resultInt, ok := result.(Integer)
	if !ok {
		t.Fatalf("Environment.Get() result type = %T, want Integer", result)
	}

	if resultInt.Value != 2 {
		t.Errorf("Environment.Get() result.Value = %d, want 2", resultInt.Value)
	}
}

func TestEnvironmentGetUndefined(t *testing.T) {
	env := NewEnvironment()

	_, err := env.Get("undefined")
	if err == nil {
		t.Error("Environment.Get() expected error for undefined variable, got nil")
	}

	expectedErr := "undefined: undefined"
	if err.Error() != expectedErr {
		t.Errorf("Environment.Get() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestEnvironmentChainLookup(t *testing.T) {
	outer := NewEnvironment()
	outer.Set("x", Integer{Value: 1})

	inner := NewEnclosedEnvironment(outer)
	inner.Set("y", Integer{Value: 2})

	// Get variable from inner environment
	resultY, err := inner.Get("y")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}
	resultYInt, ok := resultY.(Integer)
	if !ok || resultYInt.Value != 2 {
		t.Errorf("Environment.Get() y = %v, want 2", resultY)
	}

	// Get variable from outer environment
	resultX, err := inner.Get("x")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}
	resultXInt, ok := resultX.(Integer)
	if !ok || resultXInt.Value != 1 {
		t.Errorf("Environment.Get() x = %v, want 1", resultX)
	}
}

func TestEnvironmentShadowing(t *testing.T) {
	outer := NewEnvironment()
	outer.Set("x", Integer{Value: 1})

	inner := NewEnclosedEnvironment(outer)
	inner.Set("x", Integer{Value: 2})

	// Inner environment should see shadowed value
	result, err := inner.Get("x")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}
	resultInt, ok := result.(Integer)
	if !ok || resultInt.Value != 2 {
		t.Errorf("Environment.Get() x = %v, want 2", result)
	}

	// Outer environment should still have original value
	result, err = outer.Get("x")
	if err != nil {
		t.Fatalf("Environment.Get() error = %v", err)
	}
	resultInt, ok = result.(Integer)
	if !ok || resultInt.Value != 1 {
		t.Errorf("Environment.Get() x = %v, want 1", result)
	}
}

func TestEnvironmentDeepChain(t *testing.T) {
	env1 := NewEnvironment()
	env1.Set("a", Integer{Value: 1})

	env2 := NewEnclosedEnvironment(env1)
	env2.Set("b", Integer{Value: 2})

	env3 := NewEnclosedEnvironment(env2)
	env3.Set("c", Integer{Value: 3})

	// Test all variables are accessible from deepest level
	tests := []struct {
		name  string
		value int64
	}{
		{"a", 1},
		{"b", 2},
		{"c", 3},
	}

	for _, tt := range tests {
		result, err := env3.Get(tt.name)
		if err != nil {
			t.Fatalf("Environment.Get(%q) error = %v", tt.name, err)
		}
		resultInt, ok := result.(Integer)
		if !ok || resultInt.Value != tt.value {
			t.Errorf("Environment.Get(%q) = %v, want %d", tt.name, result, tt.value)
		}
	}
}

func TestEnvironmentGetChainUndefined(t *testing.T) {
	outer := NewEnvironment()
	outer.Set("x", Integer{Value: 1})

	inner := NewEnclosedEnvironment(outer)

	_, err := inner.Get("y")
	if err == nil {
		t.Error("Environment.Get() expected error for undefined variable, got nil")
	}

	expectedErr := "undefined: y"
	if err.Error() != expectedErr {
		t.Errorf("Environment.Get() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestEnvironmentStoresAnyExprType(t *testing.T) {
	env := NewEnvironment()

	env.Set("int", Integer{Value: 42})
	env.Set("sym", Symbol{Name: "foo"})
	env.Set("list", List{Items: []Expr{Integer{Value: 1}}})
	env.Set("string", String{Value: "hello"})
	env.Set("nil", &Nil{})

	tests := []struct {
		name string
		check func(Expr) bool
	}{
		{
			"int",
			func(e Expr) bool {
				i, ok := e.(Integer)
				return ok && i.Value == 42
			},
		},
		{
			"sym",
			func(e Expr) bool {
				s, ok := e.(Symbol)
				return ok && s.Name == "foo"
			},
		},
		{
			"list",
			func(e Expr) bool {
				l, ok := e.(List)
				return ok && len(l.Items) == 1
			},
		},
		{
			"string",
			func(e Expr) bool {
				s, ok := e.(String)
				return ok && s.Value == "hello"
			},
		},
		{
			"nil",
			func(e Expr) bool {
				_, ok := e.(*Nil)
				return ok
			},
		},
	}

	for _, tt := range tests {
		result, err := env.Get(tt.name)
		if err != nil {
			t.Fatalf("Environment.Get(%q) error = %v", tt.name, err)
		}
		if !tt.check(result) {
			t.Errorf("Environment.Get(%q) = %v, check failed", tt.name, result)
		}
	}
}