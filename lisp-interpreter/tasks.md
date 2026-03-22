# Lisp Interpreter - Tasks

**Project:** lisp-interpreter
**Date:** 2025-03-22

---

## Types & Environment

- [X] T001 Define AST types in types.go ✓
- [X] T002 Implement Environment in environment.go ✓

## Core Components

- [X] T003 Implement Lexer in lexer.go ✓
- [X] T004 Implement Parser in parser.go ✓
- [X] T005 Implement Evaluator core in evaluator.go ✓
- [X] T006 Add built-in functions ✓
- [X] T007 Add special forms ✓

## Entry Point

- [X] T008 Create main.go with REPL ✓

## Testing

- [X] T009 Write unit tests ✓

---

## Status Summary

```
Types:      2/2  ✓
Lexer:      1/1  ✓
Parser:     1/1  ✓
Evaluator:  1/1  ✓
Builtins:   1/1  ✓
Forms:      1/1  ✓
REPL:       1/1  ✓
Tests:      1/1  ✓
─────────────────
Total:      9/9  (100%)
```

## Test Results

```
$ (def x 10)
10

$ (+ x 5)
15

$ (defn square (n) (* n n))
#<function>

$ (square 7)
49

$ (defn factorial (n)
    (if (= n 0) 1 (* n (factorial (- n 1)))))
#<function>

$ (factorial 5)
120
```

## Files

```
lisp-interpreter/
├── cmd/lisp/main.go      # REPL entry point
├── lisp/
│   ├── types.go          # AST types
│   ├── environment.go    # Variable bindings
│   ├── lexer.go          # Tokenizer
│   ├── lexer_test.go     # Lexer tests
│   ├── parser.go         # S-expression parser
│   ├── parser_test.go    # Parser tests
│   └── evaluator.go      # Evaluator + built-ins
├── bin/lisp              # Compiled binary
├── spec.md               # Specification
├── plan.md               # Implementation plan
└── tasks.md              # This file
```