# Lisp Interpreter Specification

**Date:** 2025-03-22
**Author:** EDD Workflow

## Overview

A minimal Lisp interpreter in Go, supporting basic S-expressions, evaluation, and built-in functions.

## Supported Features

### Core Language

- [ ] `+`, `-`, `*`, `/` - Arithmetic operations
- [ ] `>`, `<`, `>=`, `<=`, `=` - Comparison operators
- [ ] `def` - Define variables
- [ ] `defn` - Define functions
- [ ] `if` - Conditional
- [ ] `let` - Local bindings
- [ ] `lambda` / `fn` - Anonymous functions

### Data Types

- [ ] Integer
- [ ] Symbol
- [ ] List
- [ ] String (optional)

### Special Forms

- [ ] Quote (`'`) - Prevent evaluation
- [ ] `cond` - Multi-way condition

## Example Programs

```lisp
; Arithmetic
(+ 1 2 3)  ; => 6

; Variables
(def x 10)
(+ x 5)   ; => 15

; Functions
(defn factorial [n]
  (if (= n 0)
    1
    (* n (factorial (- n 1)))))

(factorial 5)  ; => 120
```

## Technical Design

### Architecture

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   Lexer     │ →  │   Parser    │ →  │  Evaluator  │
│  (tokens)   │    │  (AST)      │    │  (eval)     │
└─────────────┘    └─────────────┘    └─────────────┘
```

### REPL

- [ ] Simple command-line REPL
- [ ] `:quit` or `Ctrl+D` to exit
- [ ] Print result of each expression

## File Structure

```
lisp-interpreter/
├── main.go           # Entry point, REPL
├── lexer.go          # Tokenizer
├── parser.go         # S-expression parser
├── evaluator.go      # Core evaluator
├── environment.go    # Variable/function bindings
├── types.go          # Type definitions
└── README.md
```

## Testing

- [ ] Unit tests for lexer
- [ ] Unit tests for parser
- [ ] Unit tests for evaluator
- [ ] Integration tests (REPL)

## Success Criteria

- [ ] Can evaluate basic arithmetic
- [ ] Can define and call functions
- [ ] Can run factorial example
- [ ] All tests pass