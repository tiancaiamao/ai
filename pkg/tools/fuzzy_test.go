package tools

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b   string
		min, max int
	}{
		{"hello", "hello", 0, 0},
		{"hello", "hallo", 1, 1},
		{"hello", "world", 4, 5},
		{"", "hello", 5, 5},
		{"hello", "", 5, 5},
	}
	for _, tt := range tests {
		result := levenshtein(tt.a, tt.b)
		if result < tt.min || result > tt.max {
			t.Errorf("levenshtein(%q, %q) = %d, want between %d and %d", tt.a, tt.b, result, tt.min, tt.max)
		}
	}
}

func TestNormalizeForFuzzy(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"hello world", "helloworld"},
		{"hello\tworld", "helloworld"},
		{"hello\nworld", "helloworld"},
		{"he\u2018llo", "hello"},
		{"he\u2019llo", "hello"},
	}
	for _, tt := range tests {
		result := normalizeForFuzzy(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeForFuzzy(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestFindBestMatch(t *testing.T) {
	tests := []struct {
		name    string
		content string
		target  string
		wantOK  bool
	}{
		{"exact", "hello world", "hello world", true},
		{"whitespace_diff", "hello  world", "hello world", true},
		{"unicode_confusables", "he\u2018llo", "hello", true},
		{"not_found", "hello world", "xyz", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := findBestMatch(tt.content, tt.target)
			if (err == nil) != tt.wantOK {
				t.Errorf("findBestMatch() error = %v, wantOK %v", err, tt.wantOK)
			}
		})
	}
}