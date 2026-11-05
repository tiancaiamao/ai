package run

import (
	"slices"
	"testing"
)

// The serve child must never be handed a deadline. A wall-clock timeout armed
// at process start kills whichever turn happens to be running — on a serve
// driven by repeated `ai send` that is the second task, not the first. The
// timeout belongs to the spawner (see RunSubcommand), so this guards against
// quietly re-adding the forwarding.
func TestServeArgsCarryNoTimeout(t *testing.T) {
	args := serveArgs(ServeConfig{
		Session:      "/tmp/session",
		SystemPrompt: "prompt",
		MaxTurns:     3,
		HTTP:         ":6060",
		Name:         "child",
		Role:         "coder",
		Model:        "provider/model",
	}, "/tmp/run.id")

	if slices.Contains(args, "--timeout") {
		t.Fatalf("serveArgs() = %v, must not contain --timeout", args)
	}

	for _, want := range [][2]string{
		{"serve", "--id-file"},
		{"--max-turns", "3"},
		{"--role", "coder"},
		{"--model", "provider/model"},
	} {
		i := slices.Index(args, want[0])
		if i < 0 {
			t.Fatalf("serveArgs() = %v, missing %q", args, want[0])
		}
		if want[1] != "" && (i+1 >= len(args) || args[i+1] != want[1]) {
			t.Fatalf("serveArgs() = %v, want %q followed by %q", args, want[0], want[1])
		}
	}
}
