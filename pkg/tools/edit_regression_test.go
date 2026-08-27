package tools

import (
	"strings"
	"testing"
)

// Regression tests for the tinyactor failure class: several nearly
// identical helpers in one file. A drifted oldText must never be
// silently fuzzy-matched onto the wrong sibling.

const nearDupContent = `(define (warn-count lines)
  (length (filter (lambda (line) (string-prefix? "[WARN]" line)) lines)))

(define (debug-count lines)
  (length (filter (lambda (line) (string-prefix? "[DEBUG]" line)) lines)))
`

// Drifted oldText: model copied the debug-count body but tagged it WARN
// (or vice versa). No exact or normalized match exists.
func TestFindMatch_NearDuplicateDriftIsRejected(t *testing.T) {
	drifted := `(define (debug-count lines)
  (length (filter (lambda (line) (string-prefix? "[WARN]" line)) lines)))`

	m, err := findMatch(nearDupContent, drifted)
	if err == nil {
		t.Fatalf("drifted oldText must not silently match; got strategy=%s range=[%d,%d]",
			m.strategy, m.start, m.end)
	}
	if !strings.Contains(err.Error(), "could not find") {
		t.Errorf("expected clear not-found error, got: %v", err)
	}
}

// Same shape but correct text must hit the right window via exact match.
func TestFindMatch_NearDuplicateExactHitsRightWindow(t *testing.T) {
	target := `(define (debug-count lines)
  (length (filter (lambda (line) (string-prefix? "[DEBUG]" line)) lines)))`

	m, err := findMatch(nearDupContent, target)
	if err != nil {
		t.Fatalf("exact text should match: %v", err)
	}
	if m.strategy != "exact" {
		t.Fatalf("expected exact strategy, got %s", m.strategy)
	}
	got := nearDupContent[m.start:m.end]
	if !strings.Contains(got, "debug-count") {
		t.Fatalf("matched wrong sibling window: %q", got)
	}
}

// Prefix drift inside an indentation-sensitive block stays rejected:
// leading whitespace differences are NOT normalized away.
func TestFindMatch_IndentationDriftRejected(t *testing.T) {
	wrongIndent := `  (define (debug-count lines)
    (length (filter (lambda (line) (string-prefix? "[DEBUG]" line)) lines)))`
	if _, err := findMatch(nearDupContent, wrongIndent); err == nil {
		t.Fatal("indentation-drifted oldText must be rejected, not fuzzy-matched")
	}
}
