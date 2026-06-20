package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFindLastAgentEndFast_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	result := FindLastAgentEndFast(eventsPath)
	if result != nil {
		t.Fatalf("expected nil for empty file, got %+v", result)
	}
}

func TestFindLastAgentEndFast_NoAgentEnd(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	content := `{"type":"server_start"}
{"type":"agent_start","eventAt":1234}
{"type":"text_delta","text":"hello"}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	result := FindLastAgentEndFast(eventsPath)
	if result != nil {
		t.Fatalf("expected nil when no agent_end, got %+v", result)
	}
}

func TestFindLastAgentEndFast_SuccessEnd(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	content := `{"type":"server_start"}
{"type":"agent_start","eventAt":1234}
{"type":"text_delta","text":"hello"}
{"type":"agent_end","success":true}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	result := FindLastAgentEndFast(eventsPath)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Found {
		t.Error("expected Found=true")
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
}

func TestFindLastAgentEndFast_ErrorEnd(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	content := `{"type":"agent_end","success":false,"error":"something went wrong"}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	result := FindLastAgentEndFast(eventsPath)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected Success=false")
	}
	if result.Error != "something went wrong" {
		t.Errorf("expected error 'something went wrong', got %q", result.Error)
	}
}

func TestFindLastAgentEndFast_LastOfMultiple(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	content := `{"type":"agent_end","success":true}
{"type":"text_delta","text":"second run"}
{"type":"agent_end","success":false,"error":"failed"}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	result := FindLastAgentEndFast(eventsPath)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected last agent_end to have Success=false")
	}
}

func TestFindLastAgentEndFast_LargeFile(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")

	// Write a file larger than 1MB, with agent_end at the very end.
	f, err := os.Create(eventsPath)
	if err != nil {
		t.Fatal(err)
	}

	// Write filler lines (~1.5MB)
	line := `{"type":"text_delta","text":"some filler text that makes the line longer than usual"}` + "\n"
	for i := 0; i < 30000; i++ {
		f.WriteString(line)
	}

	// Write the agent_end at the end
	endEvt := map[string]any{
		"type":    "agent_end",
		"success": true,
		"turns":   5,
	}
	endLine, _ := json.Marshal(endEvt)
	f.Write(endLine)
	f.WriteString("\n")
	f.Close()

	result := FindLastAgentEndFast(eventsPath)
	if result == nil {
		t.Fatal("expected non-nil result for large file")
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.Turns != 5 {
		t.Errorf("expected Turns=5, got %d", result.Turns)
	}
}

func TestFindLastAgentEndFast_NoFile(t *testing.T) {
	result := FindLastAgentEndFast("/nonexistent/path/events.jsonl")
	if result != nil {
		t.Fatalf("expected nil for nonexistent file, got %+v", result)
	}
}
