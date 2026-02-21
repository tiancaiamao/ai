---
name: cora
description: Lisp-1 language with pattern matching, macros, and partial application
---

# Cora Language Skill

## Overview

Use this skill when:
- Writing Cora code
- Debugging Cora programs
- Answering questions about Cora syntax/semantics
- Converting code from other Lisps to Cora

**⚠️ Start with [Common Pitfalls](references/pitfalls.md)** - most errors come from syntax confusion.

## Instructions

1. **Before writing code**: Check [pitfalls.md](references/pitfalls.md) for common mistakes
2. **Syntax reference**: Use [cora.md](references/cora.md) for complete language spec
3. **When in doubt**: Test incrementally, Cora errors can be cryptic

### Key Syntax Differences from Other Lisps

| Feature | Cora | Common Lisp/Scheme |
|---------|------|-------------------|
| let | `(let a 1 b 2 body)` | `(let ((a 1) (b 2)) body)` |
| list literal | `[a b c]` | `'(a b c)` |
| match arrows | no `=>` | N/A |
| func arrows | uses `=>` | N/A |
| docstrings | none | supported |

### Quick Reference

```lisp
;; 4 special forms
'x (if t a b) (do a b) (lambda (x) body)

;; func (uses =>)
(func fib
  0 => 0
  1 => 1
  n => (+ (fib (- n 1)) (fib (- n 2))))

;; match (NO =>)
(match lst
  [] 0
  [x . xs] (+ 1 (length xs)))

;; package
(package "my/pkg"
  (import "cora/lib/json")
  (export foo)
  (defun foo (x) ...))
```

## Examples

### Define and export a function
```lisp
(package "myapp/utils"
  (export greet)
  (defun greet (name)
    (display "Hello, ")
    (display name)
    (display "\n")))
```

### Pattern matching with JSON
```lisp
(import "cora/lib/json")

(let data (json-load-file "config.json")
  (let host (json-string-value (json-object-get data "host"))
    (display host)))
```

### Tail-recursive loop
```lisp
(defun sum (n acc)
  (if (= n 0)
      acc
      (sum (- n 1) (+ n acc))))

(sum 100 0)  ;; => 5050
```

## Reference Documents

| Document | Purpose |
|----------|---------|
| [pitfalls.md](references/pitfalls.md) | Common mistakes - READ FIRST |
| [cora.md](references/cora.md) | Complete language specification |

## Standard Libraries

| Module | Description |
|--------|-------------|
| `cora/lib/json` | JSON parsing/encoding |
| `cora/lib/string` | String operations |
| `cora/lib/os` | File system, env vars |
| `cora/lib/test` | Testing framework |
| `cora/lib/hash-h` | Hash tables |