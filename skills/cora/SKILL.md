---
name: cora
description: Cora is a Lisp-1 language with pattern matching, macros, and partial application. Use this skill when writing, debugging, or analyzing Cora code, or answering questions about Cora syntax and semantics.
---

# Cora Quick Reference

## Core Syntax

**4 Special Forms Only** (everything else is function/macro):
- `'x` or `(quote x)` — no evaluation (ONLY for symbols, NOT for lists)
- `(if test then else)` — strict booleans only (`true`/`false`)
- `(do a b)` — sequence, return `b`
- `(lambda (x) body)` — returns closure

**Syntactic Sugar**:
- `[]` → `(list)`, `[x y]` → `(list x y)`, `[x . y]` → `(list-rest x y)`
- `` `(...) `` → backquote, `,x` → unquote

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

---

# Standard Library

## Importing Modules

```lisp
(import "cora/lib/io")
(import "cora/lib/string")
(import "cora/lib/os")
(import "cora/lib/alist")
(import "cora/lib/test")
(import "cora/lib/http/util")
```

## String Operations (`cora/lib/string`)

```lisp
(import "cora/lib/string")

;; Basic operations
(string-length "hello")           ; => 5
(string-slice "hello" 1 4)        ; => "ell"
(string-index "hello" 0)          ; => "h"

;; Searching
(has-prefix? "hello" "he")        ; => true
(string-contain? "hello" "ell")   ; => true
(has-suffix? "test.txt" ".txt")   ; => true

;; Transformation
(string-upcase "hello")           ; => "HELLO"
(string-downcase "HELLO")         ; => "hello"
(string-trim "  hi  ")           ; => "hi"
(string-trim-left "  hi")         ; => "hi"
(string-trim-right "hi  ")       ; => "hi"

;; Split and join
(string-split "a,b,c" ",")        ; => ["a" "b" "c"]
(string-join '("a" "b" "c") ",")  ; => "a,b,c"

;; Replacement
(string-replace "hello" "ell" "ipp") ; => "hippo"

;; Comparison
(compare "apple" "banana")        ; => negative

;; Conversion
(number->string 42)               ; => "42"
(string->list "hi")               ; => [104 105] (ASCII values)
(byte-ref "hello" 0)              ; => 104 (ASCII for 'h')
```

## File System Operations (`cora/lib/os`)

```lisp
(import "cora/lib/os")

;; File existence and type
(file-exists? "path/to/file")     ; => true/false
(file-is-directory? "path")       ; => true/false

;; Read and write
(file-read-all "data.txt")        ; => file content as string
(file-write-all "out.txt" "data") ; => bytes written

;; Directory operations
(list-directory ".")               ; => list of filenames
(create-directory "new-dir")      ; => 0 on success
(delete-file "temp.txt")          ; => 0 on success

;; Process execution (existing)
(exec ["ls" "-l"])                ; => exit code
(popen ["echo" "hello"] 'r)       ; => file handle
(pclose handle)                   ; => exit code
```

## Association Lists (`cora/lib/alist`)

```lisp
(import "cora/lib/alist")

;; Create association list
(def headers '(("Content-Type" "text/html")
               ("Content-Length" "123")))

;; Operations
(acons "Cache-Control" "no-cache" headers)  ; => add new pair
(assq "Content-Type" headers)               ; => ("Content-Type" "text/html")
(alist-delete "Content-Length" headers)     ; => remove key
(alist-keys headers)                        ; => ("Content-Type" "Content-Length")
(alist-values headers)                      ; => ("text/html" "123")

;; Debug
(pp '(("a" . 1) ("b" . 2)))               ; pretty print with newline
```

## HTTP Utilities (`cora/lib/http/util`)

```lisp
(import "cora/lib/http/util")

;; URL encoding/decoding
(url-encode "hello world")           ; => "hello+world"
(url-decode "hello+world")           ; => "hello world"
(url-encode "user@example.com")      ; => "user%40example.com"

;; Query strings
(parse-query-string "a=1&b=2")      ; => '(("a" "1") ("b" "2"))
(build-query-string '(("a" "1") ("b" "2"))) ; => "a=1&b=2"

;; MIME types
(mime-type "index.html")             ; => "text/html"
(mime-type "style.css")              ; => "text/css"
(mime-type "app.js")                 ; => "application/javascript"
(mime-type "data.json")              ; => "application/json"
```

## Testing (`cora/lib/test`)

```lisp
(import "cora/lib/test")

(test-begin "My Test Suite")

;; Assertions
(assert-equal "addition" 5 (+ 2 3))
(assert-true "positive" (> 5 0))
(assert-false "negative" (< 5 0))
(assert-not-equal "different" 1 2)
;; (assert-error "throws" some-function) ; needs runtime support

;; End and show summary
(test-end)  ; => true if all passed, false otherwise
```

## Hash Tables (`cora/lib/hash-h`)

```lisp
(import "cora/lib/hash-h")

(hash-to-number "key")              ; => hash value
(mod 10 3)                          ; => 1
```

## JSON (`cora/lib/json`)

```lisp
(import "cora/lib/json")

;; JSON encoding/decoding
(json-encode obj)                   ; => JSON string
(json-decode str)                   ; => Cora object
```

---

# Common Idioms and Patterns

## List Processing

```lisp
;; Map
(func map
  f [] => []
  f [x . xs] => [(f x) . (map f xs)])

;; Filter
(func filter
  pred [] => []
  pred [x . xs] => [x . (filter pred xs)] where (pred x)
  pred [_ . xs] => (filter pred xs))

;; Fold (left)
(func foldl
  f acc [] => acc
  f acc [x . xs] => (foldl f (f acc x) xs))

;; Reverse
(func reverse
  acc [] => acc
  acc [x . xs] => (reverse [x . acc] xs))
```

## String Building

```lisp
;; Join strings with separator
(import "cora/lib/string")
(string-join '("Hello" "World" "!") " ")  ; => "Hello World !"

;; Build from parts
(string-slice str 0 10)  ; substring
```

## Error Handling

```lisp
;; Using try/throw for error handling
(func safe-divide
  _ 0 => (throw "Division by zero")
  x y => (/ x y))

(try (lambda () (safe-divide 10 2))
     (lambda (err resume)
       (display "Error: ")
       (display err)))
```

## File Processing

```lisp
(import "cora/lib/io")
(import "cora/lib/os")

;; Read file, process, write
(func process-file
  in-path out-path =>
    (let content (file-read-all in-path)
         processed (transform content)
         (file-write-all out-path processed)))

;; Check if file exists before reading
(func safe-read
  path => (if (file-exists? path)
               (file-read-all path)
               ""))
```

## HTTP Header Handling

```lisp
(import "cora/lib/alist")

;; Get header value
(func get-header
  name headers =>
    (let found (assq name headers)
      (if (null? found)
          ""
          (cdr found))))

;; Set header
(func set-header
  name value headers =>
    (acons name value (alist-delete name headers)))

;; Merge headers
(func merge-headers
  base new =>
    (foldl (lambda (acc pair)
             (acons (car pair) (cdr pair) acc))
           base
           new))
```

---

# Debugging and Testing

## Debugging Techniques

```lisp
(import "cora/lib/alist")

;; Pretty print
(pp some-value)          ; print with newline
(pp "Debug: value=")
(pp my-var)

;; Display without newline
(import "cora/lib/io")
(display "Value: ")
(display my-var)

;; Test framework
(import "cora/lib/test")
(test-begin "Debug Test")
(assert-equal "expected" expected-value actual-value)
(test-end)
```

## Common Pitfalls

1. **Using `=>` in `match`** - only `func` uses `=>`
2. **Parentheses dotted pairs** - use `[x . y]` not `(x . y)`
3. **Non-boolean conditions** - `if` requires `true`/`false`
4. **Quote on lists** - use `[...]` not `'(...)`
5. **Variable arguments** - operators are binary, use nesting

---

# Performance Considerations

## Tail Call Optimization

```lisp
;; Good - tail recursive
(func factorial
  acc 0 => acc
  acc n => (factorial (* acc n) (- n 1)))

;; Bad - not tail recursive
(func factorial
  0 => 1
  n => (* n (factorial (- n 1))))
```

## List Accumulation

```lisp
;; Use accumulator pattern for efficiency
(func reverse-acc
  acc [] => acc
  acc [x . xs] => (reverse-acc [x . acc] xs))

;; Avoid repeated append operations
;; Bad: (append result [new-item])
;; Good: use accumulator [new-item . acc] then reverse
```

---

# Module Structure

## Creating a Module

```lisp
(package "mylib/mymodule"
  (import "cora/lib/io")
  (export func1 func2)

  (defun func1 (x) x)
  (defun func2 (x) x))
```

## Using a Module

```lisp
(import "mylib/mymodule")

;; Direct use
(func1 42)

;; Explicit namespace
(mylib/mymodule#func1 42)
```

---

## Key Constraints

- ✅ Only `true`/`false` are booleans
- ✅ `set` only accepts quoted symbols or `gensym`
- ✅ `let` is sequential (let*)
- ✅ Unquoted symbols in patterns = variables, `'sym` = constant
- ✅ Backquote only supports `,` (no `,@`)
- ✅ Macros are unhygienic (use `gensym`)
- ✅ Quote ONLY for symbols, NOT for lists - use `[...]` for lists

---

## Full Specification

**[references/cora.md](references/cora.md)** — Complete language documentation
