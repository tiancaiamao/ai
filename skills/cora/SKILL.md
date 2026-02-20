---
name: cora
description: Cora is a Lisp-1 language with pattern matching, macros, and partial application. Use this skill when writing, debugging, or analyzing Cora code, or answering questions about Cora syntax and semantics.
---

# Cora Quick Reference

## Core Syntax

**4 Special Forms Only** (everything else is function/macro):
- `'x` or `(quote x)` — no evaluation
- `(if test then else)` — strict booleans only (`true`/`false`)
- `(do a b)` — sequence, return `b`
- `(lambda (x) body)` — returns closure

**Syntactic Sugar**:
- `[]` → `(list)`, `[x y]` → `(list x y)`, `[x . y]` → `(list-rest x y)`
- `x` → `(quote x)`, `` `(...) `` → backquote, `,x` → unquote

**Critical**: `[a . b]` ≠ `(a . b)` — only brackets support dotted pairs!

## `func` vs `match`

| | Syntax | Arrows |
|---|--------|--------|
| `func` | Function definition | Uses `=>` |
| `match` | Expression matching | No `=>` |

```lisp
;; func
(func length
  () => 0
  [_ . xs] => (+ 1 (length xs)))

;; match
(match lst
  [] 0
  [x . y] (f x y))
```

## Pattern Syntax

```lisp
()           ;; empty list
[x y z]      ;; 3-element list
[x . xs]     ;; head + tail
_            ;; any value (conventional)
'foo         ;; symbol constant (quoted)
sym          ;; variable binding (unquoted)
```

**Rule**: Use `[x . y]` in patterns, NOT `(x . y)`

## `where` Guards (Only in `func`)

```lisp
(func filter
  pred [] => []
  pred [x . xs] => [x . (filter pred xs)] where (pred x))
```

Format: `pattern => expr where condition` (at end).

## Partial Application

Insufficient args = partial function, excess args = continue applying.

```lisp
((+ 1) 2)        ; => 3
(apply + [1 2])  ; => 3 (use apply for variadic)
```

**Note**: `+ - * /` are binary, not variadic.

## `try` / `throw`

```lisp
(try (lambda () (throw 42))
     (lambda (v resume) v))         ; => 42

(try (lambda () (+ (throw 1) 2))
     (lambda (v resume)
       (resume (+ v 10))))          ; => 13
```

## Common Errors (Must Check)

| Error | Wrong | Correct |
|-------|-------|---------|
| `=>` in `match` | `(match x [] => 0)` | `(match x [] 0)` |
| Parentheses dotted pair | `(match p (x . y) ...)` | `(match p [x . y] ...)` |
| `set` with variable | `(let x 1 (set x 2))` | `(set 'sym val)` or `(set (gensym) val)` |
| Non-boolean condition | `(if 1 2 3)` | `(if true 2 3)` |
| Wrong where position | `[x] where (> x 0) => x` | `[x] => x where (> x 0)` |
| `where` in `match` | `(match x [a] (where ...))` | Use `func` for `where` |
| Symbol constant matching | `(match 'foo x ...)` | `(match 'foo 'foo ...)` |
| Variable arg function | `(+ 1 2 3)` | `(+ (+ 1 2) 3)` |

## Key Constraints

- ✅ Only `true`/`false` are booleans
- ✅ `set` only accepts quoted symbols or `gensym`
- ✅ `let` is sequential (let*)
- ✅ Unquoted symbols in patterns = variables, `'sym` = constant
- ✅ Backquote only supports `,` (no `,@`)
- ✅ Macros are unhygienic (use `gensym`)

## Modules

```lisp
(package "demo/math" (export f) (defun f (x) x))
(import "demo/math")
(cora/lib/io#display "hello")  ; explicit
```

## List Operations

```lisp
(cons x xs)        → [x . xs]
(car (x . y))      → x
(cdr (x . y))      → y
(cons? x)          → true/false
(list x y z)       → [x y z]
(list-rest x y z)  → [x y . z]
(apply f [x y])    → (f x y)
```

## I/O

```lisp
(import "cora/lib/io")
(display "hello")
```

---

## Full Specification

**[references/cora.md](references/cora.md)** — Complete language documentation including:
- Evaluation model (reader → macroexpand → eval)
- All error types
- Detailed pattern rules
- Global symbols (`def`, `defun`, `set`, `value`)
- Namespace resolution
- Full constraints checklist