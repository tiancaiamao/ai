# Lisp Interpreter - Implementation Plan

## Phase 1: Core Types

**Task 1: Define AST types**
- Create `types.go` with all type definitions
- Implement `Expr` interface
- Implement `String()` for debugging

**Task 2: Implement Environment**
- Create `environment.go`
- Store variables and functions
- Support nested scopes (for closures)

## Phase 2: Lexer

**Task 3: Tokenizer**
- Split input into tokens
- Handle: `(`, `)`, numbers, symbols, strings
- Ignore whitespace and comments (optional)

## Phase 3: Parser

**Task 4: S-Expression Parser**
- Parse tokens into AST
- Handle nested lists
- Error handling for mismatched parens

## Phase 4: Evaluator

**Task 5: Core Evaluator**
- Implement `Eval` function
- Handle symbol lookup
- Handle function application

**Task 6: Built-in Functions**
- Arithmetic: `+`, `-`, `*`, `/`
- Comparison: `>`, `<`, `>=`, `<=`, `=`
- List: `list`, `car`, `cdr`, `cons` (optional)

**Task 7: Special Forms**
- `def` - variable definition
- `defn` - function definition
- `if` - conditional
- `lambda` / `fn` - anonymous functions
- `let` - local bindings
- `quote` / `'` - prevent evaluation

## Phase 5: REPL

**Task 8: Main Entry Point**
- Create `main.go`
- Simple read-eval-print loop
- Handle `:quit` command

## Phase 6: Testing

**Task 9: Unit Tests**
- Test lexer tokenization
- Test parser
- Test evaluator with known expressions

## Execution Order

```
Types → Lexer → Parser → Evaluator → REPL → Tests
```

Can implement lexer, parser, evaluator in parallel (independent).
REPL depends on evaluator.
Tests run after each component.