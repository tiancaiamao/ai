package tools

import (
	"testing"
)

func TestComputeLineHash(t *testing.T) {
	tests := []struct {
		line     string
		wantLen  int
	}{
		{"hello world", 4},
		{"", 4},
		{"  spaces  ", 4},
		{"\ttabs\t", 4},
		{"func main() {", 4},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			hash := computeLineHash(tt.line)
			if len(hash) != tt.wantLen {
				t.Errorf("computeLineHash(%q) = %q, want length %d", tt.line, hash, tt.wantLen)
			}
			// Verify it's valid base36
			for _, c := range hash {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
					t.Errorf("computeLineHash(%q) = %q, contains non-base36 character %c", tt.line, hash, c)
				}
			}
		})
	}
}

func TestComputeLineHashConsistency(t *testing.T) {
	// Same content should produce same hash
	h1 := computeLineHash("hello world")
	h2 := computeLineHash("hello world")
	if h1 != h2 {
		t.Errorf("computeLineHash not consistent: %q != %q", h1, h2)
	}

	// Whitespace differences should produce same hash
	h3 := computeLineHash("helloworld")
	h4 := computeLineHash("hello world")
	h5 := computeLineHash("  hello  world  ")
	if h3 != h4 || h4 != h5 {
		t.Errorf("computeLineHash should ignore whitespace: %q, %q, %q", h3, h4, h5)
	}
}

func TestFormatHashLines(t *testing.T) {
	content := "line1\nline2\nline3"
	result := FormatHashLines(content, 1)

	lines := splitLines(result)
	if len(lines) != 3 {
		t.Fatalf("FormatHashLines returned %d lines, want 3", len(lines))
	}

	// Check format: LINENUM:HASH|CONTENT
	for i, line := range lines {
		expected := formatHashLine(i+1, "line"+string(rune('1'+i)))
		if line != expected {
			t.Errorf("Line %d: got %q, want format LINENUM:HASH|CONTENT", i+1, line)
		}
	}
}

func TestFormatHashLinesEmpty(t *testing.T) {
	// Empty content should return empty string, not a spurious hashline
	result := FormatHashLines("", 1)
	if result != "" {
		t.Errorf("FormatHashLines(\"\", 1) = %q, want empty string", result)
	}
}

func TestParseLineRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantLine int
		wantHash string
		wantErr  bool
	}{
		{"5:abc1", 5, "abc1", false},
		{"1:xyz9", 1, "xyz9", false},
		{"  10:ab12  ", 10, "ab12", false},
		{">>>5:abc1", 5, "abc1", false},
		{">>5:abc1", 5, "abc1", false},
		{"invalid", 0, "", true},
		{"5:", 0, "", true},
		{":abc", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			ref, err := ParseLineRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseLineRef(%q) expected error, got nil", tt.ref)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseLineRef(%q) unexpected error: %v", tt.ref, err)
				return
			}
			if ref.Line != tt.wantLine {
				t.Errorf("ParseLineRef(%q).Line = %d, want %d", tt.ref, ref.Line, tt.wantLine)
			}
			if ref.Hash != tt.wantHash {
				t.Errorf("ParseLineRef(%q).Hash = %q, want %q", tt.ref, ref.Hash, tt.wantHash)
			}
		})
	}
}

func TestApplyHashlineEdits(t *testing.T) {
	t.Run("set_line", func(t *testing.T) {
		content := "line1\nline2\nline3"
		hash := computeLineHash("line2")

		edits := []HashlineEdit{{
			Type:    HashlineEditSetLine,
			Anchor:  "2:" + hash,
			NewText: "modified",
		}}

		result, err := ApplyHashlineEdits(edits, content)
		if err != nil {
			t.Fatalf("ApplyHashlineEdits error: %v", err)
		}

		expected := "line1\nmodified\nline3"
		if result.Content != expected {
			t.Errorf("ApplyHashlineEdits = %q, want %q", result.Content, expected)
		}
	})

	t.Run("hash_mismatch", func(t *testing.T) {
		content := "line1\nline2\nline3"

		edits := []HashlineEdit{{
			Type:    HashlineEditSetLine,
			Anchor:  "2:wrong",
			NewText: "modified",
		}}

		_, err := ApplyHashlineEdits(edits, content)
		if err == nil {
			t.Error("ApplyHashlineEdits expected error for hash mismatch")
		}

		var mismatchErr *HashlineMismatchError
		if !errorAs(err, &mismatchErr) {
			t.Errorf("ApplyHashlineEdits error = %T, want *HashlineMismatchError", err)
		}
	})

	t.Run("insert_after", func(t *testing.T) {
		content := "line1\nline2\nline3"
		hash := computeLineHash("line2")

		edits := []HashlineEdit{{
			Type:   HashlineEditInsertAfter,
			Anchor: "2:" + hash,
			Text:   "inserted",
		}}

		result, err := ApplyHashlineEdits(edits, content)
		if err != nil {
			t.Fatalf("ApplyHashlineEdits error: %v", err)
		}

		expected := "line1\nline2\ninserted\nline3"
		if result.Content != expected {
			t.Errorf("ApplyHashlineEdits = %q, want %q", result.Content, expected)
		}
	})

	t.Run("replace_lines_range", func(t *testing.T) {
		content := "line1\nline2\nline3\nline4"
		hash2 := computeLineHash("line2")
		hash3 := computeLineHash("line3")

		edits := []HashlineEdit{{
			Type:         HashlineEditReplaceLines,
			StartAnchor:  "2:" + hash2,
			EndAnchor:    "3:" + hash3,
			NewText:      "replaced",
		}}

		result, err := ApplyHashlineEdits(edits, content)
		if err != nil {
			t.Fatalf("ApplyHashlineEdits error: %v", err)
		}

		expected := "line1\nreplaced\nline4"
		if result.Content != expected {
			t.Errorf("ApplyHashlineEdits = %q, want %q", result.Content, expected)
		}
	})

	t.Run("replace_substring", func(t *testing.T) {
		content := "line1\nline2\nline3"

		edits := []HashlineEdit{{
			Type:    HashlineEditReplace,
			OldText: "line2",
			NewText: "modified",
			All:     false,
		}}

		result, err := ApplyHashlineEdits(edits, content)
		if err != nil {
			t.Fatalf("ApplyHashlineEdits error: %v", err)
		}

		expected := "line1\nmodified\nline3"
		if result.Content != expected {
			t.Errorf("ApplyHashlineEdits = %q, want %q", result.Content, expected)
		}
	})

	t.Run("replace_substring_all", func(t *testing.T) {
		content := "foo bar foo baz foo"

		edits := []HashlineEdit{{
			Type:    HashlineEditReplace,
			OldText: "foo",
			NewText: "qux",
			All:     true,
		}}

		result, err := ApplyHashlineEdits(edits, content)
		if err != nil {
			t.Fatalf("ApplyHashlineEdits error: %v", err)
		}

		expected := "qux bar qux baz qux"
		if result.Content != expected {
			t.Errorf("ApplyHashlineEdits = %q, want %q", result.Content, expected)
		}
	})
}

func TestValidateLineRef(t *testing.T) {
	lines := []string{"line1", "line2", "line3"}

	t.Run("valid", func(t *testing.T) {
		hash := computeLineHash("line2")
		ref := &LineRef{Line: 2, Hash: hash}
		err := ValidateLineRef(ref, lines)
		if err != nil {
			t.Errorf("ValidateLineRef error: %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		ref := &LineRef{Line: 2, Hash: "wrong"}
		err := ValidateLineRef(ref, lines)
		if err == nil {
			t.Error("ValidateLineRef expected error for mismatch")
		}
	})

	t.Run("out_of_range", func(t *testing.T) {
		ref := &LineRef{Line: 10, Hash: "abc"}
		err := ValidateLineRef(ref, lines)
		if err == nil {
			t.Error("ValidateLineRef expected error for out of range")
		}
	})
}

// Helper functions

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	result := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func formatHashLine(num int, content string) string {
	hash := computeLineHash(content)
	return formatInt(num) + ":" + hash + "|" + content
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if neg {
		digits = append(digits, '-')
	}
	// Reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

func errorAs(err error, target interface{}) bool {
	return err != nil && errorAsImpl(err, target)
}

func errorAsImpl(err error, target interface{}) bool {
	switch e := err.(type) {
	case *HashlineMismatchError:
		if p, ok := target.(**HashlineMismatchError); ok {
			*p = e
			return true
		}
	}
	return false
}