package lisp

import "fmt"

// Environment holds variable and function bindings.
type Environment struct {
	outer *Environment
	data  map[string]Expr
}

// NewEnvironment creates a new empty environment.
func NewEnvironment() *Environment {
	return &Environment{
		data: make(map[string]Expr),
	}
}

// NewEnclosedEnvironment creates a new environment with an outer (parent) scope.
func NewEnclosedEnvironment(outer *Environment) *Environment {
	return &Environment{
		outer: outer,
		data:  make(map[string]Expr),
	}
}

// Get looks up a name in the environment chain.
func (e *Environment) Get(name string) (Expr, error) {
	if expr, ok := e.data[name]; ok {
		return expr, nil
	}
	if e.outer != nil {
		return e.outer.Get(name)
	}
	return nil, fmt.Errorf("undefined: %s", name)
}

// Set assigns a value to a name in the current environment.
func (e *Environment) Set(name string, expr Expr) {
	e.data[name] = expr
}