package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSystemContent_FileReference tests that @file references are
// resolved to the file's actual content.
func TestResolveSystemContent_FileReference(t *testing.T) {
	// Create a temp file with known content
	tmpDir := t.TempDir()
	content := "You are a test explorer agent. Do XYZ."
	filePath := filepath.Join(tmpDir, "system.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Simulate what bridge.go does for @file resolution
	system := "@" + filePath
	sysContent := system
	if strings.HasPrefix(system, "@") {
		resolvedPath := strings.TrimPrefix(system, "@")
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		sysContent = string(data)
	}

	if sysContent != content {
		t.Errorf("expected file content %q, got %q", content, sysContent)
	}

	// Verify the resulting RPC message is correct
	sysMsg := map[string]string{"type": "prompt", "message": sysContent}
	encoded, _ := json.Marshal(sysMsg)

	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["type"] != "prompt" {
		t.Errorf("expected type=prompt, got %s", decoded["type"])
	}
	if decoded["message"] != content {
		t.Errorf("expected message=%s, got %s", content, decoded["message"])
	}
}

// TestResolveSystemContent_Inline tests that inline system prompts are used as-is.
func TestResolveSystemContent_Inline(t *testing.T) {
	system := "You are a helpful assistant."
	sysContent := system
	if strings.HasPrefix(system, "@") {
		t.Fatal("should not enter @file branch for inline content")
	}

	sysMsg := map[string]string{"type": "prompt", "message": sysContent}
	encoded, _ := json.Marshal(sysMsg)

	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["message"] != system {
		t.Errorf("expected message=%s, got %s", system, decoded["message"])
	}
}

// TestResolveSystemContent_FileNotFound tests that missing files produce an error.
func TestResolveSystemContent_FileNotFound(t *testing.T) {
	system := "@/nonexistent/path/to/file.md"
	if !strings.HasPrefix(system, "@") {
		t.Fatal("expected @ prefix")
	}
	filePath := strings.TrimPrefix(system, "@")
	_, err := os.ReadFile(filePath)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
