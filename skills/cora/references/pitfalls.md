# Common Pitfalls

Most Cora errors come from syntax confusion with other Lisps.

## Critical Syntax Errors

### 1. `let` syntax (MOST COMMON!)

```lisp
;; CORRECT - body in same form
(let x 1
  (display x))

;; CORRECT - multi binding (sequential)
(let a 3
  b (+ a 1)
  (+ a b))

;; WRONG - extra parens (Common Lisp style)
(let ((a 3) (b 5)) (+ a b))

;; WRONG - extra ) closes let, body outside!
(let data (json-load-file "f.json"))
  (display data))  ;; data undefined!
```

### 2. `defun` has no docstrings

```lisp
;; WRONG - string is function body
(defun foo (x) "docs" (+ x 1))

;; CORRECT
(defun foo (x) (+ x 1))
```

### 3. `match` vs `func` arrows

```lisp
;; func - uses =>
(func len
  () => 0
  [_ . xs] => (+ 1 (len xs)))

;; match - NO =>
(match lst
  [] 0
  [x . y] (f x y))
```

### 4. `load-so` vs `import`

```lisp
;; WRONG - top-level not executed
(load-so "core.so")
(display INDEX)  ;; undefined!

;; CORRECT
(import "myapp/core")
```

### 5. Unexported variables cause segfault

```lisp
;; In package A (INDEX not exported)
(package "A"
  (def INDEX [1 2 3])
  (export foo))

;; WRONG - segfault, not friendly error
(display A#INDEX)
```

### 6. Dotted pairs need brackets

```lisp
;; CORRECT
[x . y]

;; WRONG
(x . y)
```

### 7. Quote ONLY for symbols

```lisp
;; CORRECT
'foo        ;; symbol
[a b c]     ;; list literal

;; WRONG
'(a b c)    ;; don't quote lists
```

### 8. Symbol constants in match need quote

```lisp
;; CORRECT
(match x
  'foo 1    ;; symbol constant
  _ 0)

;; WRONG - foo is variable binding
(match x
  foo 1)
```

### 9. `if` requires strict booleans

```lisp
;; WRONG
(if lst 'yes 'no)  ;; () not false!

;; CORRECT
(if (null? lst) 'yes 'no)
```

### 10. Operators are binary

```lisp
;; WRONG
(+ 1 2 3)

;; CORRECT
(+ (+ 1 2) 3)
```