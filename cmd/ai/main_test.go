package main

import (
	"os"
	"testing"
)

func TestParseSystemPrompt(t *testing.T) {
	// Test 1: Empty string
	result := parseSystemPrompt("")
	if result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}

	// Test 2: Plain text (no @ prefix)
	plainText := "You are a helpful assistant"
	result = parseSystemPrompt(plainText)
	if result != plainText {
		t.Errorf("Expected %q, got %q", plainText, result)
	}

	// Test 3: @ prefix with empty path
	result = parseSystemPrompt("@")
	if result != "" {
		t.Errorf("Expected empty string for @ with no path, got %q", result)
	}

	// Test 4: @ prefix with whitespace only
	result = parseSystemPrompt("@   ")
	if result != "" {
		t.Errorf("Expected empty string for @ with whitespace, got %q", result)
	}

	// Test 5: @ prefix with valid file
	tmpFile := t.TempDir() + "/test_prompt.md"
	content := "You are a test assistant"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result = parseSystemPrompt("@" + tmpFile)
	if result != content {
		t.Errorf("Expected %q, got %q", content, result)
	}

	// Test 6: @ prefix with non-existent file
	result = parseSystemPrompt("@/nonexistent/path/to/file.md")
	if result != "" {
		t.Errorf("Expected empty string for non-existent file, got %q", result)
	}

	// Test 7: Large file (truncate to 64KB)
	largeFile := t.TempDir() + "/large_prompt.md"
	largeContent := make([]byte, 70*1024) // 70KB
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		t.Fatalf("Failed to create large temp file: %v", err)
	}
	result = parseSystemPrompt("@" + largeFile)
	if len(result) != 64*1024 {
		t.Errorf("Expected 64KB, got %d bytes", len(result))
	}

	// Test 8: @ with leading whitespace in path
	tmpFile2 := t.TempDir() + "/test_prompt2.md"
	if err := os.WriteFile(tmpFile2, []byte("Content 2"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result = parseSystemPrompt("@   " + tmpFile2)
	if result != "Content 2" {
		t.Errorf("Expected 'Content 2', got %q", result)
	}
}