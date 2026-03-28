package llm

import (
	"testing"
)

func TestExtractFieldFromPartialJSON(t *testing.T) {
	tests := []struct {
		name      string
		jsonStr   string
		fieldName string
		want      string
	}{
		{
			name:      "extract content from truncated JSON",
			jsonStr:   `{"content": "<!DOCTYPE html>\n<html>"`,
			fieldName: "content",
			want:      "<!DOCTYPE html>\n<html>",
		},
		{
			name:      "extract path from truncated JSON",
			jsonStr:   `{"path": "test.html", "content": "incomplete`,
			fieldName: "path",
			want:      "test.html",
		},
		{
			name:      "extract command from truncated JSON",
			jsonStr:   `{"command": "ls -la", "timeout": 30`,
			fieldName: "command",
			want:      "ls -la",
		},
		{
			name:      "extract number field",
			jsonStr:   `{"timeout": 120, "path": "test"`,
			fieldName: "timeout",
			want:      "120",
		},
		{
			name:      "extract with escaped quotes",
			jsonStr:   `{"content": "line1\nline2\t\"quoted\""`,
			fieldName: "content",
			want:      "line1\nline2\t\"quoted\"",
		},
		{
			name:      "field not found",
			jsonStr:   `{"other": "value"}`,
			fieldName: "path",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFieldFromPartialJSON(tt.jsonStr, tt.fieldName)
			if got != tt.want {
				t.Errorf("extractFieldFromPartialJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTryParsePartialToolCallArguments(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantFields     map[string]string
		wantPartial    bool
	}{
		{
			name: "valid complete JSON",
			args: `{"path": "test.txt", "content": "hello"}`,
			wantFields: map[string]string{
				"path":    "test.txt",
				"content": "hello",
			},
			wantPartial: false,
		},
		{
			name: "truncated JSON with content only (real case from MiniMax)",
			args: `{"content": "<!DOCTYPE html>\n<html>\n<body>incomplete...`,
			wantFields: map[string]string{
				"content": "<!DOCTYPE html>\n<html>\n<body>incomplete...",
			},
			wantPartial: true,
		},
		{
			name: "truncated JSON with path and partial content",
			args: `{"path": "output.html", "content": "<!DOCTYPE html>...`,
			wantFields: map[string]string{
				"path":    "output.html",
				"content": "<!DOCTYPE html>...",
			},
			wantPartial: true,
		},
		{
			name: "truncated JSON with command",
			args: `{"command": "npm run build", "incomplete`,
			wantFields: map[string]string{
				"command": "npm run build",
			},
			wantPartial: true,
		},
		{
			name:        "empty string",
			args:        "",
			wantFields:  nil,
			wantPartial: false,
		},
		{
			name:        "completely invalid JSON with no extractable fields",
			args:        `{{{broken`,
			wantFields:  nil,
			wantPartial: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotPartial := tryParsePartialToolCallArguments(tt.args)

			if gotPartial != tt.wantPartial {
				t.Errorf("tryParsePartialToolCallArguments() partial = %v, want %v", gotPartial, tt.wantPartial)
			}

			if tt.wantFields == nil {
				if got != nil {
					t.Errorf("tryParsePartialToolCallArguments() expected nil, got %v", got)
				}
				return
			}

			if got == nil {
				t.Errorf("tryParsePartialToolCallArguments() expected non-nil result")
				return
			}

			for field, wantValue := range tt.wantFields {
				gotValue, ok := got[field].(string)
				if !ok {
					t.Errorf("field %q not found or not string", field)
					continue
				}
				if gotValue != wantValue {
					t.Errorf("field %q = %q, want %q", field, gotValue, wantValue)
				}
			}
		})
	}
}

func TestTryParsePartialToolCallArguments_RealCase(t *testing.T) {
	// This is the actual truncated JSON from the session trace
	truncatedJSON := `{"content": "<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Golden Gate Bridge - 3D Simulation</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { overflow: hidde`

	got, isPartial := tryParsePartialToolCallArguments(truncatedJSON)

	if !isPartial {
		t.Errorf("expected partial parse to succeed")
	}

	if got == nil {
		t.Fatalf("expected non-nil result")
	}

	content, ok := got["content"].(string)
	if !ok {
		t.Fatalf("expected content field to be string, got %T", got["content"])
	}

	// Verify the content starts with expected text
	expectedStart := "<!DOCTYPE html>"
	if len(content) < len(expectedStart) || content[:len(expectedStart)] != expectedStart {
		t.Errorf("content should start with %q, got %q", expectedStart, content[:min(100, len(content))])
	}

	t.Logf("Successfully extracted content of length %d", len(content))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}