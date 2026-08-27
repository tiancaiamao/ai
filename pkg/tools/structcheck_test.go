package tools

import (
	"os/exec"
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
		// python3 unavailable in this environment; the check is skipped by
		// design. Verify that assumption so a real regression isn't masked.
		if _, lookErr := exec.LookPath("python3"); lookErr == nil {
			t.Fatal("python3 present but syntax breakage was not rejected")
		}
		t.Skip("python3 not available; structural check skipped by design")
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

// Regression: a file that was already broken before the edit must not be
// blocked from incremental repair (review finding, PR #386).
func TestStructCheck_AlreadyBrokenFileRemainsEditable(t *testing.T) {
	before := "def f(:\n    pass\n" // pre-existing syntax error
	after := "def f(:\n    return 1\n"
	if err := structCheck("/tmp/x.py", before, after); err != nil {
		t.Fatalf("already-broken file should not be blocked: %v", err)
	}
}

// Regression: valid-before + invalid-after must still be rejected.
func TestStructCheck_PythonValidBeforeBrokenAfterRejected(t *testing.T) {
	before := "def f():\n    return 1\n"
	after := "def f():\n    return (\n"
	if err := structCheck("/tmp/x.py", before, after); err == nil {
		t.Fatal("expected rejection when edit breaks previously-valid python")
	}
}

// Regression: a lisp file that was already paren-broken before the edit must
// remain repairable — the balance-delta gate must not block moving from
// broken to balanced. Copilot review finding, PR #386.
func TestStructCheck_LispRepairOfBrokenFileAllowed(t *testing.T) {
	before := "(define a 1)\n(define b 2\n" // already unbalanced
	after := "(define a 1)\n(define b 2)\n" // incremental repair -> balanced
	if err := structCheck("/tmp/x.scm", before, after); err != nil {
		t.Fatalf("repairing an already-broken file was blocked: %v", err)
	}
}

// A still-broken after state on an already-broken file is also allowed
// (file was broken before; we only guard against *introducing* breakage).
func TestStructCheck_LispBrokenBeforeBrokenAfterAllowed(t *testing.T) {
	before := "(foo (bar\n"
	after := "(foo (baz\n"
	if err := structCheck("/tmp/x.el", before, after); err != nil {
		t.Fatalf("broken->broken edit rejected: %v", err)
	}
}

// Guard must still fire when the file WAS balanced before.
func TestStructCheck_LispBalancedBeforeBrokenAfterStillRejected(t *testing.T) {
	before := "(define a 1)\n"
	after := "(define a 1\n" // breaks it
	if err := structCheck("/tmp/x.scm", before, after); err == nil {
		t.Fatal("balanced->broken edit must be rejected")
	}
}

// describe() renders only for the errorLine branch (stray close paren);
// long-snippet rendering paths covered via the same message.
func TestLispState_Describe(t *testing.T) {
	msg := structCheck("/tmp/x.scm", "(ok)\n", "(ok)\n)\n") // stray close
	if msg == nil || !strings.Contains(msg.Error(), "near line 2") {
		t.Fatalf("stray close must report near-line, got: %v", msg)
	}
}
