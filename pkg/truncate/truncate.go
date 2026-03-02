// Package truncate provides text truncation utilities with UTF-8 safety
package truncate

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Truncate truncates text to fit within maxChars, preserving prefix and suffix.
// It uses a 50/50 split for prefix/suffix and ensures UTF-8 boundary safety.
func Truncate(text string, maxChars int) string {
	if text == "" || len(text) <= maxChars {
		return text
	}

	if maxChars <= 0 {
		return ""
	}

	// Start with a conservative marker estimate, then refine once split result
	// is known so the final output always respects maxChars.
	marker := formatTruncationMarker(ApproxTokenCount(text))
	for i := 0; i < 2; i++ {
		if len(marker) >= maxChars {
			return trimUTF8ToBytes(marker, maxChars)
		}

		budget := maxChars - len(marker)
		leftChars := budget / 2
		rightChars := budget - leftChars

		removedTokens, prefix, suffix := splitString(text, leftChars, rightChars)
		newMarker := formatTruncationMarker(removedTokens)
		result := assembleOutput(prefix, suffix, newMarker)
		if len(result) <= maxChars {
			return result
		}

		marker = newMarker
	}

	return trimUTF8ToBytes(marker, maxChars)
}

// splitString splits a string into prefix and suffix with UTF-8 boundary safety.
// Returns the number of removed tokens, prefix string, and suffix string.
func splitString(s string, beginningBytes, endBytes int) (removedTokens int, prefix, suffix string) {
	if s == "" {
		return 0, "", ""
	}

	totalLen := len(s)
	tailStartTarget := totalLen - endBytes
	if tailStartTarget < 0 {
		tailStartTarget = 0
	}

	prefixEnd := 0
	suffixStart := totalLen
	removedBytes := 0
	suffixStarted := false

	// Iterate over runes to ensure UTF-8 boundary safety
	for idx, ch := range s {
		runeLen := utf8.RuneLen(ch)
		charEnd := idx + runeLen

		if charEnd <= beginningBytes {
			prefixEnd = charEnd
			continue
		}

		if idx >= tailStartTarget {
			if !suffixStarted {
				suffixStart = idx
				suffixStarted = true
			}
			continue
		}

		removedBytes += runeLen
	}

	// Prevent overlap
	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}

	removedTokens = CharsToTokens(removedBytes)
	prefix = s[:prefixEnd]
	suffix = s[suffixStart:]

	return
}

// formatTruncationMarker formats a truncation marker with token count.
func formatTruncationMarker(removedTokens int) string {
	return fmt.Sprintf("…%d tokens truncated…", removedTokens)
}

// assembleOutput assembles the truncated output from prefix, marker, and suffix.
func assembleOutput(prefix, suffix, marker string) string {
	var sb strings.Builder
	sb.Grow(len(prefix) + len(marker) + len(suffix))
	sb.WriteString(prefix)
	sb.WriteString(marker)
	sb.WriteString(suffix)
	return sb.String()
}

// trimUTF8ToBytes trims s to at most maxBytes bytes, preserving UTF-8 validity.
func trimUTF8ToBytes(s string, maxBytes int) string {
	if maxBytes <= 0 || s == "" {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for idx, ch := range s {
		next := idx + utf8.RuneLen(ch)
		if next > maxBytes {
			break
		}
		end = next
	}
	return s[:end]
}
