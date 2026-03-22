package lisp

import "fmt"

// Expr is the interface for all AST nodes.
// Every expression type must implement Eval.
type Expr interface {
	Eval(env *Environment) (Expr, error)
	String() string
}

// Integer represents a numeric value.
type Integer struct {
	Value int64
}

// Eval returns the integer itself (self-evaluating).
func (i Integer) Eval(env *Environment) (Expr, error) {
	return i, nil
}

func (i Integer) String() string {
	return fmt.Sprintf("%d", i.Value)
}

// Symbol represents a variable or function name.
type Symbol struct {
	Name string
}

// Eval looks up the symbol in the environment.
func (s Symbol) Eval(env *Environment) (Expr, error) {
	return env.Get(s.Name)
}

func (s Symbol) String() string {
	return s.Name
}

// List represents a parenthesized S-expression.
type List struct {
	Items []Expr
}

// Eval evaluates the list: either a special form or function application.
func (l List) Eval(env *Environment) (Expr, error) {
	if len(l.Items) == 0 {
		return l, nil
	}

	// Check if it's a special form (before evaluating first element)
	if sym, ok := l.Items[0].(Symbol); ok {
		if special, ok := specialForms[sym.Name]; ok {
			return special(l.Items[1:], env)
		}
	}

	// Otherwise, evaluate the first element and treat as function application
	first, err := l.Items[0].Eval(env)
	if err != nil {
		return nil, err
	}

	return apply(first, l.Items[1:], env)
}

func (l List) String() string {
	s := "("
	for i, item := range l.Items {
		if i > 0 {
			s += " "
		}
		s += item.String()
	}
	s += ")"
	return s
}

// Function represents a user-defined or built-in function.
type Function struct {
	Params []Symbol
	Body   []Expr
	Env    *Environment // closure: captured environment
}

// Eval applies the function to arguments.
func (f Function) Eval(env *Environment) (Expr, error) {
	return f, nil // functions evaluate to themselves
}

func (f Function) String() string {
	return "#<function>"
}

// apply evaluates arguments and applies a function.
func apply(fn Expr, args []Expr, env *Environment) (Expr, error) {
	// Evaluate all arguments
	evaluated := make([]Expr, len(args))
	for i, arg := range args {
		e, err := arg.Eval(env)
		if err != nil {
			return nil, err
		}
		evaluated[i] = e
	}

	// Handle built-in functions
	if builtin, ok := fn.(*BuiltinFunc); ok {
		return builtin.Fn(env, evaluated)
	}

	// Handle user-defined functions
	if f, ok := fn.(Function); ok {
		return callFunction(f, evaluated, env)
	}

	return nil, fmt.Errorf("not a function: %s", fn)
}

// callFunction creates a new environment for the function and evaluates its body.
func callFunction(f Function, args []Expr, outer *Environment) (Expr, error) {
	if len(f.Params) != len(args) {
		return nil, fmt.Errorf("argument count mismatch: expected %d, got %d",
			len(f.Params), len(args))
	}

	// Create new environment with captured closure
	newEnv := NewEnclosedEnvironment(f.Env)

	// Bind parameters to arguments
	for i, param := range f.Params {
		newEnv.Set(param.Name, args[i])
	}

	// Evaluate body
	var result Expr
	for _, expr := range f.Body {
		var err error
		result, err = expr.Eval(newEnv)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// BuiltinFunc represents a built-in function implemented in Go.
type BuiltinFunc struct {
	Name string
	Fn   func(env *Environment, args []Expr) (Expr, error)
}

func (b *BuiltinFunc) Eval(env *Environment) (Expr, error) {
	return b, nil
}

func (b *BuiltinFunc) String() string {
	return fmt.Sprintf("#<builtin:%s>", b.Name)
}

// specialForms maps special form names to their handlers.
var specialForms = map[string]func(args []Expr, env *Environment) (Expr, error){}

// registerSpecialForm registers a special form handler.
func registerSpecialForm(name string, fn func(args []Expr, env *Environment) (Expr, error)) {
	specialForms[name] = fn
}

// globalEnv is the global environment for the interpreter.
var globalEnv = NewEnvironment()

// RegisterBuiltin registers a built-in function in the global environment.
func RegisterBuiltin(name string, fn func(env *Environment, args []Expr) (Expr, error)) {
	globalEnv.Set(name, &BuiltinFunc{Name: name, Fn: fn})
}

// RegisterSpecialForm registers a special form handler (public API).
func RegisterSpecialForm(name string, fn func(args []Expr, env *Environment) (Expr, error)) {
	registerSpecialForm(name, fn)
}

// GlobalEnv returns the global environment where built-ins are registered.
func GlobalEnv() *Environment {
	return globalEnv
}