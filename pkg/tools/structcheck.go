package tools

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// structCheck validates that an edit/write did not introduce structural damage
// (unbalanced parens in Lisp-family files, syntax errors in Python/YAML).
// It compares against the pre-edit state so that already-broken files are not
// blocked from being edited further.
//
// Rationale: weak models frequently produce newText with imbalanced parens or
// broken indentation. Without an immediate, localized error the model only
// discovers the damage later via a confusing interpreter failure (often far
// from the real bug), which leads it to abandon surgical edits and rewrite the
// whole file — destroying unrelated code. The sentinel converts that late,
// misleading failure into an immediate, precise one.
func structCheck(fullPath string, before, after string) error {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fullPath), "."))

	switch ext {
	case "scm", "ss", "lisp", "el", "cl", "rkt", "sld":
		bb, ba := lispParens(before), lispParens(after)
		if ba.balance == bb.balance && ba.errorLine < 0 {
			return nil // no regression introduced
		}
		if ba.errorLine >= 0 {
			return fmt.Errorf(
				"structural check failed: this edit makes parentheses unbalanced (%s)",
				ba.describe())
		}
		return fmt.Errorf(
			"structural check failed: parenthesis balance changed from %d to %d; "+
				"the edit added or removed unmatched parens. Count your ( and ) in newText.",
			bb.balance, ba.balance)

	case "py":
		return externalSyntaxCheck(fullPath, "python", after,
			[]string{"-c", "import sys,ast;ast.parse(sys.stdin.read())"})
	case "yaml", "yml":
		return externalSyntaxCheck(fullPath, "python", after,
			[]string{"-c", "import sys,yaml;yaml.safe_load(sys.stdin)"})
	}
	return nil
}

// externalSyntaxCheck pipes content to an external syntax checker if the
// interpreter is available. Missing interpreters silently skip the check.
func externalSyntaxCheck(fullPath, interp string, content string, extraArgs []string) error {
	interpreter := interp
	if _, err := exec.LookPath("python3"); err == nil {
		interpreter = "python3"
	} else if _, err := exec.LookPath(interp); err != nil {
		return nil // no checker available; skip
	}

	cmd := exec.Command(interpreter, extraArgs...)
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return fmt.Errorf("structural check failed: edited file has invalid %s syntax:\n%s",
		strings.ToLower(whatLang(fullPath)), detail)
}

func whatLang(path string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "py":
		return "Python"
	case "yaml", "yml":
		return "YAML"
	}
	return "file"
}

// lispState reports paren-balance status of Lisp-family source text.
type lispState struct {
	balance   int // open parens minus close parens (outside strings/comments)
	errorLine int // 1-based line where structural error detected (-1 if none)
	reason    string
}

func (s lispState) describe() string {
	line := s.errorLine
	if line < 0 {
		line = 0
	}
	return s.reason + fmt.Sprintf(" (near line %d)", line)
}

// lispParens scans Lisp source handling: ";" line comments, "#|...|#" block
// comments, "#;" datum comments (treated like block-open until end of next
// sexp is not tracked — conservatively we skip just the datum token), string
// literals with escapes, and character literals like #\( or #\\.
func lispParens(src string) lispState {
	st := lispState{errorLine: -1}
	line := 1
	i := 0
	n := len(src)

	inString := false
	inBlockComment := 0 // nesting count for #| |#
	inLineComment := false

	for i < n {
		c := src[i]

		if c == '\n' {
			line++
			inLineComment = false
			i++
			continue
		}

		if inLineComment {
			i++
			continue
		}

		if inString {
			if c == '\\' && i+1 < n {
				i += 2
				continue
			}
			if c == '"' {
				inString = false
			}
			i++
			continue
		}

		if inBlockComment > 0 {
			if c == '|' && i+1 < n && src[i+1] == '#' {
				inBlockComment--
				i += 2
				continue
			}
			i++
			continue
		}

		switch {
		case c == ';':
			inLineComment = true
			i++
		case c == '"':
			inString = true
			i++
		case c == '#' && i+1 < n && src[i+1] == '|':
			inBlockComment++
			i += 2
		case c == '#' && i+1 < n && src[i+1] == '\\':
			// char literal: skip "#\" plus the char name / single char
			j := i + 2
			for j < n && isDelimiterChar(src[j]) == false {
				j++
			}
			if j == i+2 && j < n {
				j++ // "#\(" — single delimiter char literal
			}
			line += strings.Count(src[i:j], "\n")
			i = j
		case c == '(' || c == '[':
			st.balance++
			i++
		case c == ')' || c == ']':
			st.balance--
			if st.balance < 0 {
				if st.errorLine < 0 {
					st.errorLine = line
					st.reason = "unexpected closing paren"
				}
				st.balance = 0 // resync instead of cascading negatives
			}
			i++
		default:
			i++
		}
	}

	if inString && st.errorLine < 0 {
		st.errorLine = line
		st.reason = "unterminated string literal"
	}
	if inBlockComment > 0 && st.errorLine < 0 {
		st.errorLine = line
		st.reason = "unterminated block comment (#| without |#)"
	}
	return st
}

func isDelimiterChar(c byte) bool {
	switch c {
	case '(', ')', '[', ']', '"', ';', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
