// Package tools provides hashline edit functionality.
//
// Hashline edit mode is a line-addressable edit format using content hashes.
// Each line in a file is identified by its 1-indexed line number and a short
// base36 hash derived from the normalized line content (xxHash32, truncated to 4
// base36 chars).
//
// The combined LINE:HASH reference acts as both an address and a staleness check:
// if the file has changed since the caller last read it, hash mismatches are caught
// before any mutation occurs.
//
// Displayed format: LINENUM:HASH|CONTENT
// Reference format: "LINENUM:HASH" (e.g. "5:a3f2")
package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"
)

const (
	// HASH_LEN is the number of base36 characters in the hash
	HASH_LEN = 4
	// HASH_MOD is 36^4 = 1679616
	HASH_MOD = 1679616
)

// Base36 alphabet for hash encoding
const BASE36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// computeLineHash computes a short content-hash for a line.
// The hash is derived from xxHash64 of the whitespace-normalized line content,
// truncated to 4 base36 characters.
// The line input should not include a trailing newline.
func computeLineHash(line string) string {
	// Remove trailing \r if present
	line = strings.TrimSuffix(line, "\r")
	// Remove all whitespace
	line = removeWhitespace(line)

	// Compute xxHash64
	h := xxhash.Sum64String(line)

	// Convert to base36 with 4 characters
	idx := h % HASH_MOD
	return intToBase36(uint32(idx), HASH_LEN)
}

// removeWhitespace removes all whitespace characters from a string
func removeWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// intToBase36 converts an integer to a fixed-length base36 string
func intToBase36(n uint32, length int) string {
	result := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		result[i] = BASE36[n%36]
		n /= 36
	}
	return string(result)
}

// FormatHashLines formats file content with hashline prefixes for display.
// Each line becomes LINENUM:HASH|CONTENT where LINENUM is 1-indexed.
func FormatHashLines(content string, startLine int) string {
	// Handle empty content - return empty output instead of spurious hashline
	if content == "" {
		return ""
	}
	
	if startLine == 0 {
		startLine = 1
	}
	lines := strings.Split(content, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		num := startLine + i
		hash := computeLineHash(line)
		result[i] = fmt.Sprintf("%d:%s|%s", num, hash, line)
	}
	return strings.Join(result, "\n")
}

// LineRef represents a parsed LINE:HASH reference
type LineRef struct {
	Line int
	Hash string
}

// ParseLineRef parses a "LINE:HASH" reference string
func ParseLineRef(ref string) (*LineRef, error) {
	// Handle optional prefixes like ">>>" or ">>"
	re := regexp.MustCompile(`^\s*(?:>>>|>>)?\s*(\d+):([0-9a-zA-Z]{1,16})`)
	matches := re.FindStringSubmatch(ref)
	if matches == nil {
		return nil, fmt.Errorf("invalid line reference: %q", ref)
	}

	line, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, fmt.Errorf("invalid line number: %q", matches[1])
	}

	return &LineRef{
		Line: line,
		Hash: matches[2],
	}, nil
}

// HashMismatch represents a single hash mismatch found during validation
type HashMismatch struct {
	Line     int    // 1-indexed line number
	Expected string // Hash the caller provided
	Actual   string // Hash computed from current file content
}

// HashlineMismatchError is returned when hash validation fails
type HashlineMismatchError struct {
	Mismatches []HashMismatch
	Remaps     map[int]string // Quick-fix mapping of stale line references to corrected hashes
}

func (e *HashlineMismatchError) Error() string {
	if len(e.Mismatches) == 1 {
		m := e.Mismatches[0]
		return fmt.Sprintf("hash mismatch at line %d: expected %s, got %s", m.Line, m.Expected, m.Actual)
	}
	return fmt.Sprintf("%d hash mismatches detected", len(e.Mismatches))
}

// ValidateLineRef validates that a line reference matches the current file content
func ValidateLineRef(ref *LineRef, fileLines []string) error {
	if ref.Line < 1 || ref.Line > len(fileLines) {
		return fmt.Errorf("line %d out of range (1-%d)", ref.Line, len(fileLines))
	}

	actual := computeLineHash(fileLines[ref.Line-1])
	if actual != ref.Hash {
		return &HashlineMismatchError{
			Mismatches: []HashMismatch{{
				Line:     ref.Line,
				Expected: ref.Hash,
				Actual:   actual,
			}},
			Remaps: map[int]string{ref.Line: actual},
		}
	}
	return nil
}

// HashlineEditType defines the type of hashline edit operation
type HashlineEditType int

const (
	HashlineEditSetLine HashlineEditType = iota
	HashlineEditReplaceLines
	HashlineEditInsertAfter
	HashlineEditReplace
)

// HashlineEdit represents a single hashline edit operation
type HashlineEdit struct {
	Type HashlineEditType

	// For set_line
	Anchor  string // "LINE:HASH"
	NewText string

	// For replace_lines
	StartAnchor string // "LINE:HASH"
	EndAnchor   string // "LINE:HASH" (optional, can be empty for single line)

	// For insert_after
	Text string

	// For replace (substr-style)
	OldText string
	All     bool
}

// HashlineEditResult represents the result of applying hashline edits
type HashlineEditResult struct {
	Content          string
	FirstChangedLine int
	Warnings         []string
	NoopEdits        []NoopEdit
}

// NoopEdit represents an edit that produced no changes
type NoopEdit struct {
	EditIndex     int
	Location      string
	CurrentContent string
}

// ApplyHashlineEdits applies a series of hashline edits to file content.
// It validates all hashes before making any changes, ensuring atomicity.
func ApplyHashlineEdits(edits []HashlineEdit, content string) (*HashlineEditResult, error) {
	if len(edits) == 0 {
		return &HashlineEditResult{Content: content}, nil
	}

	fileLines := strings.Split(content, "\n")
	originalFileLines := make([]string, len(fileLines))
	copy(originalFileLines, fileLines)

	// Track which lines are explicitly touched
	explicitlyTouchedLines := make(map[int]bool)

	// First pass: validate all hashes and parse edits
	type parsedEdit struct {
		idx        int
		spec       *parsedSpec
		dst        string
		dstLines   []string
	}
	
	var parsedEdits []parsedEdit
	var mismatches []HashMismatch
	remaps := make(map[int]string)

	for idx, edit := range edits {
		// Handle replace mode separately
		if edit.Type == HashlineEditReplace {
			// Replace is handled separately, skip validation
			parsedEdits = append(parsedEdits, parsedEdit{
				idx:      idx,
				spec:     &parsedSpec{kind: "replace"},
				dst:      edit.OldText + "|||" + edit.NewText,
				dstLines: []string{edit.OldText, edit.NewText},
			})
			continue
		}

		spec, err := parseHashlineEdit(edit)
		if err != nil {
			return nil, fmt.Errorf("edit %d: %w", idx, err)
		}

		// Validate hashes
		switch spec.kind {
		case "single":
			if err := validateHash(spec.ref, originalFileLines, &mismatches, remaps); err != nil {
				return nil, err
			}
			explicitlyTouchedLines[spec.ref.Line] = true
		case "range":
			if err := validateHash(spec.start, originalFileLines, &mismatches, remaps); err != nil {
				return nil, err
			}
			if err := validateHash(spec.end, originalFileLines, &mismatches, remaps); err != nil {
				return nil, err
			}
			for ln := spec.start.Line; ln <= spec.end.Line; ln++ {
				explicitlyTouchedLines[ln] = true
			}
		case "insertAfter":
			if err := validateHash(spec.after, originalFileLines, &mismatches, remaps); err != nil {
				return nil, err
			}
			explicitlyTouchedLines[spec.after.Line] = true
		}

		dstLines := splitDstLines(spec.dst)
		parsedEdits = append(parsedEdits, parsedEdit{
			idx:      idx,
			spec:     spec,
			dst:      spec.dst,
			dstLines: dstLines,
		})
	}

	// If any mismatches, return error with remaps
	if len(mismatches) > 0 {
		return nil, &HashlineMismatchError{
			Mismatches: mismatches,
			Remaps:     remaps,
		}
	}

	// Second pass: apply edits (in reverse order to preserve line numbers)
	// Sort by line number descending
	// Note: We need to apply in a specific order to handle dependencies

	var firstChangedLine int
	var warnings []string
	var noopEdits []NoopEdit

	// Apply edits from last to first to preserve line numbers
	for i := len(parsedEdits) - 1; i >= 0; i-- {
		pe := parsedEdits[i]

		switch pe.spec.kind {
		case "single":
			origLine := originalFileLines[pe.spec.ref.Line-1]
			newLines := pe.dstLines

			// Restore indentation
			newLines = restoreIndentForPairedReplacement([]string{origLine}, newLines)

			// Check if this is a no-op
			if strings.Join(newLines, "\n") == origLine {
				noopEdits = append(noopEdits, NoopEdit{
					EditIndex:     pe.idx,
					Location:      fmt.Sprintf("%d:%s", pe.spec.ref.Line, pe.spec.ref.Hash),
					CurrentContent: origLine,
				})
				continue
			}

			// Replace single line
			if len(newLines) == 1 {
				fileLines[pe.spec.ref.Line-1] = newLines[0]
			} else {
				// Replace with multiple lines
				fileLines = spliceSlice(fileLines, pe.spec.ref.Line-1, 1, newLines)
			}
			trackFirstChanged(&firstChangedLine, pe.spec.ref.Line)

		case "range":
			count := pe.spec.end.Line - pe.spec.start.Line + 1
			origLines := originalFileLines[pe.spec.start.Line-1 : pe.spec.start.Line-1+count]
			newLines := pe.dstLines

			// Restore indentation
			newLines = restoreIndentForPairedReplacement(origLines, newLines)

			// Check if this is a no-op
			if strings.Join(newLines, "\n") == strings.Join(origLines, "\n") {
				noopEdits = append(noopEdits, NoopEdit{
					EditIndex:     pe.idx,
					Location:      fmt.Sprintf("%d:%s", pe.spec.start.Line, pe.spec.start.Hash),
					CurrentContent: strings.Join(origLines, "\n"),
				})
				continue
			}

			fileLines = spliceSlice(fileLines, pe.spec.start.Line-1, count, newLines)
			trackFirstChanged(&firstChangedLine, pe.spec.start.Line)

		case "insertAfter":
			insertLine := pe.spec.after.Line
			inserted := pe.dstLines

			if len(inserted) == 0 {
				noopEdits = append(noopEdits, NoopEdit{
					EditIndex:     pe.idx,
					Location:      fmt.Sprintf("%d:%s", pe.spec.after.Line, pe.spec.after.Hash),
					CurrentContent: originalFileLines[pe.spec.after.Line-1],
				})
				continue
			}

			fileLines = spliceSlice(fileLines, insertLine, 0, inserted)
			trackFirstChanged(&firstChangedLine, insertLine+1)

		case "replace":
			// Handle replace mode - operates on full content, not line-based
			if len(pe.dstLines) >= 2 {
				oldText := pe.dstLines[0]
				newText := pe.dstLines[1]
				fileContent := strings.Join(fileLines, "\n")
				
				// Check if this is a no-op
				if !strings.Contains(fileContent, oldText) {
					noopEdits = append(noopEdits, NoopEdit{
						EditIndex:     pe.idx,
						Location:      "replace",
						CurrentContent: oldText,
					})
					continue
				}
				
				// Apply the replacement
				if edit, ok := findEditByIndex(edits, pe.idx); ok && edit.All {
					fileContent = strings.ReplaceAll(fileContent, oldText, newText)
				} else {
					fileContent = strings.Replace(fileContent, oldText, newText, 1)
				}
				
				fileLines = strings.Split(fileContent, "\n")
				trackFirstChanged(&firstChangedLine, 1)
			}
		}
	}

	// Check for excessive changes
	diffLineCount := absInt(len(fileLines) - len(originalFileLines))
	for i := 0; i < minInt(len(fileLines), len(originalFileLines)); i++ {
		if fileLines[i] != originalFileLines[i] {
			diffLineCount++
		}
	}
	if diffLineCount > len(edits)*4 {
		warnings = append(warnings, 
			fmt.Sprintf("Edit changed %d lines across %d operations — verify no unintended reformatting.", 
				diffLineCount, len(edits)))
	}

	return &HashlineEditResult{
		Content:          strings.Join(fileLines, "\n"),
		FirstChangedLine: firstChangedLine,
		Warnings:         warnings,
		NoopEdits:        noopEdits,
	}, nil
}

type parsedSpec struct {
	kind  string
	ref   *LineRef
	start *LineRef
	end   *LineRef
	after *LineRef
	dst   string
}

func parseHashlineEdit(edit HashlineEdit) (*parsedSpec, error) {
	switch edit.Type {
	case HashlineEditSetLine:
		ref, err := ParseLineRef(edit.Anchor)
		if err != nil {
			return nil, err
		}
		return &parsedSpec{
			kind: "single",
			ref:  ref,
			dst:  edit.NewText,
		}, nil

	case HashlineEditReplaceLines:
		start, err := ParseLineRef(edit.StartAnchor)
		if err != nil {
			return nil, err
		}
		if edit.EndAnchor == "" {
			return &parsedSpec{
				kind: "single",
				ref:  start,
				dst:  edit.NewText,
			}, nil
		}
		end, err := ParseLineRef(edit.EndAnchor)
		if err != nil {
			return nil, err
		}
		if start.Line == end.Line {
			return &parsedSpec{
				kind: "single",
				ref:  start,
				dst:  edit.NewText,
			}, nil
		}
		return &parsedSpec{
			kind:  "range",
			start: start,
			end:   end,
			dst:   edit.NewText,
		}, nil

	case HashlineEditInsertAfter:
		after, err := ParseLineRef(edit.Anchor)
		if err != nil {
			return nil, err
		}
		return &parsedSpec{
			kind:  "insertAfter",
			after: after,
			dst:   edit.Text,
		}, nil

	default:
		return nil, fmt.Errorf("unknown edit type: %d", edit.Type)
	}
}

func validateHash(ref *LineRef, fileLines []string, mismatches *[]HashMismatch, remaps map[int]string) error {
	if ref.Line < 1 || ref.Line > len(fileLines) {
		return fmt.Errorf("line %d out of range (1-%d)", ref.Line, len(fileLines))
	}

	actual := computeLineHash(fileLines[ref.Line-1])
	if actual != ref.Hash {
		*mismatches = append(*mismatches, HashMismatch{
			Line:     ref.Line,
			Expected: ref.Hash,
			Actual:   actual,
		})
		remaps[ref.Line] = actual
	}
	return nil
}

func splitDstLines(dst string) []string {
	if dst == "" {
		return []string{}
	}
	return strings.Split(dst, "\n")
}

func restoreIndentForPairedReplacement(oldLines, newLines []string) []string {
	if len(oldLines) != len(newLines) {
		return newLines
	}

	changed := false
	out := make([]string, len(newLines))
	for i, line := range newLines {
		restored := restoreLeadingIndent(oldLines[i], line)
		out[i] = restored
		if restored != line {
			changed = true
		}
	}

	if !changed {
		return newLines
	}
	return out
}

func restoreLeadingIndent(templateLine, line string) string {
	if len(line) == 0 {
		return line
	}
	templateIndent := leadingWhitespace(templateLine)
	if len(templateIndent) == 0 {
		return line
	}
	indent := leadingWhitespace(line)
	if len(indent) > 0 {
		return line
	}
	return templateIndent + line
}

func leadingWhitespace(s string) string {
	match := regexp.MustCompile(`^\s*`).FindString(s)
	return match
}

func spliceSlice[T any](s []T, index, count int, newItems []T) []T {
	result := make([]T, 0, len(s)-count+len(newItems))
	result = append(result, s[:index]...)
	result = append(result, newItems...)
	result = append(result, s[index+count:]...)
	return result
}

func trackFirstChanged(firstChangedLine *int, line int) {
	if *firstChangedLine == 0 || line < *firstChangedLine {
		*firstChangedLine = line
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findEditByIndex finds an edit by its original index in the edits slice
func findEditByIndex(edits []HashlineEdit, idx int) (HashlineEdit, bool) {
	if idx >= 0 && idx < len(edits) {
		return edits[idx], true
	}
	return HashlineEdit{}, false
}