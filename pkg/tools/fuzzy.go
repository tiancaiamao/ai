package tools

import (
	"strings"
	"unicode"
)

// matchResult holds a fuzzy match result
type matchResult struct {
	start int
	end   int
	score int
}

// findBestMatch finds the best match using layered fuzzy matching.
// Returns PoorMatchError if match quality is poor but still usable.
func findBestMatch(content, target string) (*matchResult, error) {
	contentLines := strings.Split(content, "\n")
	targetLines := strings.Split(target, "\n")

	// Pass 1: Exact match
	if match := findExactMatch(content, targetLines); match != nil {
		return match, nil
	}

	// Pass 2: Normalized exact match (unicode + whitespace)
	if match := findNormalizedMatch(content, target); match != nil {
		return match, nil
	}

	// Pass 3: Prefix match
	if match := findPrefixMatch(contentLines, targetLines); match != nil {
		return match, nil
	}

	// Pass 4: Substring match
	if match := findSubstringMatch(contentLines, targetLines); match != nil {
		return match, nil
	}

	// Pass 5: Fuzzy similarity (Levenshtein-based)
	if match := findFuzzyMatch(contentLines, targetLines); match != nil {
		if match.score <= 10 {
			return match, nil
		}
		// Poor match but still usable
		return nil, &PoorMatchError{
			Match:   match,
			Message: "fuzzy match is poor (score " + itoa(match.score) + ")",
		}
	}

	// Pass 6: Function signature fallback
	if match := findFuncSigMatch(content, target); match != nil {
		return match, nil
	}

	return nil, &PoorMatchError{
		Match:   nil,
		Message: "could not find matching text",
	}
}

// findExactMatch tries to find an exact byte match
func findExactMatch(content string, targetLines []string) *matchResult {
	target := strings.Join(targetLines, "\n")
	idx := strings.Index(content, target)
	if idx == -1 {
		return nil
	}
	return &matchResult{start: idx, end: idx + len(target), score: 0}
}

// normalizeForFuzzy normalizes a string for fuzzy matching
func normalizeForFuzzy(s string) string {
	// Handle common unicode confusables first
	s = strings.ReplaceAll(s, "\u2018", "")  // left single quote
	s = strings.ReplaceAll(s, "\u2019", "")  // right single quote
	s = strings.ReplaceAll(s, "\u201c", "")  // left double quote
	s = strings.ReplaceAll(s, "\u201d", "")  // right double quote
	s = strings.ReplaceAll(s, "\u2013", "-")  // en dash
	s = strings.ReplaceAll(s, "\u2014", "-")  // em dash
	s = strings.ReplaceAll(s, "\u00a0", " ")  // non-breaking space
	s = strings.ReplaceAll(s, "\u200b", "")  // zero-width space

	// Lowercase
	s = strings.ToLower(s)

	// Remove all whitespace
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findNormalizedMatch finds match ignoring whitespace differences
func findNormalizedMatch(content, target string) *matchResult {
	targetNorm := normalizeForFuzzy(target)
	if targetNorm == "" {
		return nil
	}

	// Find all occurrences and verify each
	lines := strings.Split(target, "\n")
	pos := 0
	for pos < len(content) {
		idx := strings.Index(content[pos:], target)
		if idx == -1 {
			break
		}
		actualPos := pos + idx

		if verifyMatchAt(content, actualPos, lines) {
			return &matchResult{start: actualPos, end: actualPos + len(target), score: 0}
		}
		pos = actualPos + 1
	}

	return nil
}

// verifyMatchAt verifies that targetLines match at the given position
func verifyMatchAt(content string, start int, targetLines []string) bool {
	pos := start
	for _, line := range targetLines {
		eol := pos
		for eol < len(content) && content[eol] != '\n' {
			eol++
		}

		contentLine := strings.TrimRight(content[pos:eol], " \t")
		targetLine := strings.TrimRight(line, " \t")

		if normalizeForFuzzy(contentLine) != normalizeForFuzzy(targetLine) {
			return false
		}

		pos = eol + 1
		if pos > len(content) {
			pos = len(content)
		}
	}
	return true
}

// findPrefixMatch finds lines that start with target
func findPrefixMatch(contentLines, targetLines []string) *matchResult {
	if len(targetLines) == 0 {
		return nil
	}

	firstLine := strings.TrimRight(targetLines[0], " \t")
	if firstLine == "" {
		return nil
	}

	for i := 0; i < len(contentLines); i++ {
		trimmedContentLine := strings.TrimRight(contentLines[i], " \t")
		if !strings.HasPrefix(trimmedContentLine, firstLine) {
			continue
		}

		match := true
		endLine := i
		for j := 1; j < len(targetLines); j++ {
			endLine++
			if endLine >= len(contentLines) || contentLines[endLine] != targetLines[j] {
				match = false
				break
			}
		}

		if match {
			startPos := 0
			for k := 0; k < i; k++ {
				startPos += len(contentLines[k]) + 1
			}
			endPos := startPos
			for k := i; k <= endLine; k++ {
				endPos += len(contentLines[k]) + 1
			}
			return &matchResult{start: startPos, end: endPos - 1, score: 1}
		}
	}

	return nil
}

// findSubstringMatch finds lines that contain target as substring
func findSubstringMatch(contentLines, targetLines []string) *matchResult {
	if len(targetLines) == 0 {
		return nil
	}

	firstLine := strings.TrimRight(targetLines[0], " \t")
	if len(firstLine) < 3 {
		return nil
	}

	for i := 0; i < len(contentLines); i++ {
		trimmedContentLine := strings.TrimRight(contentLines[i], " \t")
		if !strings.Contains(trimmedContentLine, firstLine) {
			continue
		}

		match := true
		endLine := i
		for j := 1; j < len(targetLines); j++ {
			endLine++
			if endLine >= len(contentLines) || contentLines[endLine] != targetLines[j] {
				match = false
				break
			}
		}

		if match {
			startPos := 0
			for k := 0; k < i; k++ {
				startPos += len(contentLines[k]) + 1
			}
			endPos := startPos
			for k := i; k <= endLine; k++ {
				endPos += len(contentLines[k]) + 1
			}
			return &matchResult{start: startPos, end: endPos - 1, score: 2}
		}
	}

	return nil
}

// findFuzzyMatch uses Levenshtein distance for fuzzy matching
func findFuzzyMatch(contentLines, targetLines []string) *matchResult {
	windowSize := len(targetLines)
	if windowSize == 0 {
		return nil
	}

	best := &matchResult{score: 999999}

	for i := 0; i <= len(contentLines)-windowSize; i++ {
		window := contentLines[i : i+windowSize]
		score := computeEditDistance(window, targetLines)

		if score < best.score {
			startPos := 0
			for k := 0; k < i; k++ {
				startPos += len(contentLines[k]) + 1
			}
			endPos := startPos
			for k := i; k < i+windowSize; k++ {
				endPos += len(contentLines[k]) + 1
			}
			best = &matchResult{start: startPos, end: endPos - 1, score: score}
		}
	}

	if best.score == 999999 {
		return nil
	}
	return best
}

// findFuncSigMatch handles function signatures with/without parens
func findFuncSigMatch(content, target string) *matchResult {
	trimmed := strings.TrimSpace(target)

	variations := []string{
		target,
		strings.TrimSuffix(trimmed, "()") + "(",
		strings.TrimSuffix(trimmed, "{}") + "{",
	}

	prefixes := []string{"func ", "function ", "def ", "async "}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(trimmed, prefix) {
			variations = append(variations, prefix+strings.TrimPrefix(trimmed, "func "))
			variations = append(variations, prefix+strings.TrimPrefix(trimmed, "function "))
			variations = append(variations, prefix+strings.TrimPrefix(trimmed, "def "))
			variations = append(variations, prefix+strings.TrimPrefix(trimmed, "async "))
		}
	}

	for _, variant := range variations {
		idx := strings.Index(content, variant)
		if idx != -1 {
			return &matchResult{start: idx, end: idx + len(variant), score: 3}
		}
	}

	return nil
}

// computeEditDistance computes total Levenshtein distance between two slices of lines
func computeEditDistance(a, b []string) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	score := 0
	for i := 0; i < maxLen; i++ {
		if i >= len(a) {
			score += len(b[i])
		} else if i >= len(b) {
			score += len(a[i])
		} else {
			score += levenshtein(a[i], b[i])
		}
	}
	return score
}

// levenshtein computes Levenshtein distance between two strings
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	lenA, lenB := len(a), len(b)
	if lenA == 0 {
		return lenB
	}
	if lenB == 0 {
		return lenA
	}

	if lenA < lenB {
		a, b = b, a
		lenA, lenB = lenB, lenA
	}

	prev := make([]int, lenB+1)
	for j := 0; j <= lenB; j++ {
		prev[j] = j
	}

	curr := make([]int, lenB+1)
	for i := 1; i <= lenA; i++ {
		curr[0] = i
		for j := 1; j <= lenB; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[lenB]
}

// min3 returns minimum of three integers
func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// itoa converts int to string
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b strings.Builder
	for i > 0 {
		b.WriteByte('0' + byte(i%10))
		i /= 10
	}
	s := b.String()
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}