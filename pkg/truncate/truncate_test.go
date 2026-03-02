package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCharsToTokens(t *testing.T) {
	tests := []struct {
		chars    int
		expected int
	}{
		{0, 0},
		{1, 1},
		{4, 1},
		{5, 2},
		{8, 2},
		{100, 25},
	}

	for _, tt := range tests {
		result := CharsToTokens(tt.chars)
		if result != tt.expected {
			t.Errorf("CharsToTokens(%d) = %d, want %d", tt.chars, result, tt.expected)
		}
	}
}

func TestTokensToChars(t *testing.T) {
	tests := []struct {
		tokens   int
		expected int
	}{
		{0, 0},
		{1, 4},
		{2, 8},
		{25, 100},
	}

	for _, tt := range tests {
		result := TokensToChars(tt.tokens)
		if result != tt.expected {
			t.Errorf("TokensToChars(%d) = %d, want %d", tt.tokens, result, tt.expected)
		}
	}
}

func TestApproxTokenCount(t *testing.T) {
	tests := []struct {
		text     string
		expected int
	}{
		{"", 0},
		{"hello", 2},       // 5 bytes / 4 = 1.25 → 2
		{"hello world", 3}, // 11 bytes / 4 = 2.75 → 3
		{"你好", 2},          // 6 bytes / 4 = 1.5 → 2
		{"😀😀😀", 3},         // 12 bytes / 4 = 3
	}

	for _, tt := range tests {
		result := ApproxTokenCount(tt.text)
		if result != tt.expected {
			t.Errorf("ApproxTokenCount(%q) = %d, want %d", tt.text, result, tt.expected)
		}
	}
}

func TestSplitString(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		beginningBytes  int
		endBytes        int
		expectValidUTF8 bool
	}{
		{
			name:            "empty string",
			text:            "",
			beginningBytes:  5,
			endBytes:        5,
			expectValidUTF8: true,
		},
		{
			name:            "ASCII",
			text:            "hello world",
			beginningBytes:  5,
			endBytes:        5,
			expectValidUTF8: true,
		},
		{
			name:            "Chinese",
			text:            "你好世界",
			beginningBytes:  6, // 1.5 Chinese characters
			endBytes:        6,
			expectValidUTF8: true,
		},
		{
			name:            "emoji",
			text:            "😀😃😄😁",
			beginningBytes:  8, // 2 emoji
			endBytes:        8,
			expectValidUTF8: true,
		},
		{
			name:            "mixed",
			text:            "hello你好world世界",
			beginningBytes:  10,
			endBytes:        10,
			expectValidUTF8: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, prefix, suffix := splitString(tt.text, tt.beginningBytes, tt.endBytes)

			// Verify UTF-8 validity
			if !utf8.ValidString(prefix) {
				t.Errorf("prefix is not valid UTF-8: %q", prefix)
			}
			if !utf8.ValidString(suffix) {
				t.Errorf("suffix is not valid UTF-8: %q", suffix)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		maxChars      int
		wantTruncated bool
	}{
		{
			name:          "empty string",
			text:          "",
			maxChars:      100,
			wantTruncated: false,
		},
		{
			name:          "no truncation needed",
			text:          "short",
			maxChars:      100,
			wantTruncated: false,
		},
		{
			name:          "needs truncation ASCII",
			text:          strings.Repeat("a", 1000),
			maxChars:      100,
			wantTruncated: true,
		},
		{
			name:          "needs truncation Chinese",
			text:          strings.Repeat("你好", 500),
			maxChars:      100,
			wantTruncated: true,
		},
		{
			name:          "needs truncation emoji",
			text:          strings.Repeat("😀", 100),
			maxChars:      100,
			wantTruncated: true,
		},
		{
			name:          "zero maxChars",
			text:          "some text",
			maxChars:      0,
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Truncate(tt.text, tt.maxChars)

			if tt.wantTruncated {
				if tt.maxChars > 0 && len(result) > tt.maxChars {
					t.Errorf("result exceeds maxChars: got %d > %d", len(result), tt.maxChars)
				}
				if tt.maxChars == 0 {
					if result != "" {
						t.Errorf("expected empty result for zero maxChars, got: %q", result)
					}
					return
				}
				if !strings.Contains(result, "tokens truncated") {
					t.Errorf("expected truncation marker in result, got: %q", result)
				}
				if !utf8.ValidString(result) {
					t.Errorf("result is not valid UTF-8: %q", result)
				}
			} else {
				if result != tt.text {
					t.Errorf("expected no truncation, got: %q", result)
				}
			}
		})
	}
}

func TestTruncateNeverExceedsMaxChars(t *testing.T) {
	text := strings.Repeat("a", 50001)
	maxChars := 50000

	result := Truncate(text, maxChars)
	if len(result) > maxChars {
		t.Fatalf("result exceeds maxChars: got %d > %d", len(result), maxChars)
	}
}

func TestTruncateIsIdempotentAtLimit(t *testing.T) {
	text := strings.Repeat("a", 50001)
	maxChars := 50000

	first := Truncate(text, maxChars)
	second := Truncate(first, maxChars)
	if len(second) > maxChars {
		t.Fatalf("idempotent truncate exceeds maxChars: got %d > %d", len(second), maxChars)
	}
	if first != second {
		t.Fatalf("truncate should be stable on already truncated input")
	}
}

func TestTruncateUTF8Boundary(t *testing.T) {
	// Test that truncation doesn't break multi-byte characters
	text := "你好世界，这是一个测试文本，用于验证UTF-8边界安全性。"
	maxChars := 20

	result := Truncate(text, maxChars)

	// Verify result is valid UTF-8
	if !utf8.ValidString(result) {
		t.Errorf("result is not valid UTF-8: %q", result)
	}

	// Verify it contains truncation marker
	if !strings.Contains(result, "truncat") {
		t.Errorf("expected truncation marker fragment, got: %q", result)
	}
}

func TestTruncatePreservesContent(t *testing.T) {
	// Test that prefix and suffix are preserved
	prefix := "PREFIX123"
	suffix := "SUFFIX456"
	middle := strings.Repeat("x", 1000)
	text := prefix + middle + suffix

	maxChars := 100
	result := Truncate(text, maxChars)

	// Should contain prefix
	if !strings.Contains(result, prefix) {
		t.Errorf("prefix not preserved in result: %q", result)
	}

	// Should contain suffix
	if !strings.Contains(result, suffix) {
		t.Errorf("suffix not preserved in result: %q", result)
	}

	// Should contain truncation marker
	if !strings.Contains(result, "tokens truncated") {
		t.Errorf("truncation marker not found in result: %q", result)
	}
}

func TestFormatTruncationMarker(t *testing.T) {
	tests := []struct {
		removedTokens int
		expected      string
	}{
		{0, "…0 tokens truncated…"},
		{10, "…10 tokens truncated…"},
		{100, "…100 tokens truncated…"},
	}

	for _, tt := range tests {
		result := formatTruncationMarker(tt.removedTokens)
		if result != tt.expected {
			t.Errorf("formatTruncationMarker(%d) = %q, want %q", tt.removedTokens, result, tt.expected)
		}
	}
}

func TestAssembleOutput(t *testing.T) {
	prefix := "hello"
	suffix := "world"
	marker := "…truncated…"

	result := assembleOutput(prefix, suffix, marker)
	expected := "hello…truncated…world"

	if result != expected {
		t.Errorf("assembleOutput() = %q, want %q", result, expected)
	}
}
