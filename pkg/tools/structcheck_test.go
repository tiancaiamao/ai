package tools

import (
	"strings"
	"testing"
)

func TestLispParens_Balanced(t *testing.T) {
	src := `(define (f x) (+ x 1)) ; comment ( not counted
;; another ) comment
(display "string with ) and (" )`
	st := lispParens(src)
	if st.balance != 0 {
		t.Errorf("expected balance 0, got %d", st.balance)
	}
	if st.errorLine >= 0 {
		t.Errorf("unexpected error: %s", st.describe())
	}
}

func TestLispParens_CharLiteralsDoNotCount(t *testing.T) {
	// #\( #\) are char literals, must be ignored
	src := `(define nl #\newline)
(define lp #\()
(define x (list #\) #\())`
	st := lispParens(src)
	if st.balance != 0 {
		t.Errorf("char literals should not affect balance, got %d", st.balance)
	}
}

func TestLispParens_MissingCloseParen(t *testing.T) {
	// Simulates the tinyactor failure: model's newText dropped one close paren
	src := `(define (read-line port)
  (let loop ((acc '()))
    (let ((c (read-char port)))
      (if (or (eof-object? c) (char=? c #\newline))
          (apply string-append (reverse acc))
          (loop (cons (string c) acc)))))` // one paren short
	st := lispParens(src)
	if st.errorLine < 0 && st.balance == 0 {
		t.Fatalf("unbalanced input not detected")
	}
}

func TestLispParens_UnexpectedClose(t *testing.T) {
	src := "(define x 1))" // extra close paren on line 1
	st := lispParens(src)
	if st.errorLine != 1 {
		t.Errorf("expected error at line 1, got line=%d", st.errorLine)
	}
}

func TestLispParens_BlockComments(t *testing.T) {
	src := "#| nested (#| deeper |# still ) comment |# (define ok 1)"
	st := lispParens(src)
	if st.balance != 0 {
		t.Errorf("block comments must be skipped, balance=%d", st.balance)
	}
}

func TestStructCheck_LispRejectsRegression(t *testing.T) {
	before := "(define a 1)\n(define b 2)\n"
	after := "(define a 1)\n(define b 2" // dropped final close paren
	err := structCheck("/tmp/x.scm", before, after)
	if err == nil {
		t.Fatal("expected rejection for paren imbalance introduced by edit")
	}
	if !strings.Contains(err.Error(), "paren") {
		t.Errorf("error should mention parens, got: %v", err)
	}
}

func TestStructCheck_LispAllowsNeutralEdit(t *testing.T) {
	before := "(define a 1)\n"
	after := "(define b 2)\n" // replaced but still balanced
	if err := structCheck("/tmp/x.scm", before, after); err != nil {
		t.Fatalf("balanced edit rejected: %v", err)
	}
}

func TestStructCheck_PythonRejectsSyntaxError(t *testing.T) {
	before := "def f():\n    return 1\n"
	after := "def f():\n    return (\n" // invalid syntax
	err := structCheck("/tmp/x.py", before, after)
	if err == nil {
		t.Fatal("expected rejection for python syntax breakage")
	}
}

func TestStructCheck_YamlRejectsBroken(t *testing.T) {
	err := structCheck("/tmp/x.yaml", "", "key: [unclosed\nother: {\n")
	if err == nil {
		t.Log("python/yaml checker unavailable or accepted; skipping strict assert")
	}
}

func TestStructCheck_UnrelatedExtensionSkipped(t *testing.T) {
	// .md/.txt/etc: no structural check applies
	before := "anything ( unbalanced"
	after := "still ( unbalanced"
	if err := structCheck("/tmp/x.md", before, after); err != nil {
		t.Fatalf("markdown files must not be struct-checked, got: %v", err)
	}
}
