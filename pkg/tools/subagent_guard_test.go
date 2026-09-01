package tools

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestBashToolBlocksNestedAgentLaunches(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := NewBashTool(ws)
	tool.subagentDepth = 1
	ctx := context.Background()

	for _, command := range []string{
		"ai serve --role reviewer",
		"/Users/genius/go/bin/ai run",
		"AI_SUBAGENT_DEPTH=0 ai serve",
		"env --unset AI_SUBAGENT_DEPTH ai serve",
		"env -- AI_SUBAGENT_DEPTH=0 ai serve",
		"command ai serve",
		"nohup ai serve",
		"setsid ai serve",
		"nohup -- ai serve",
		"command -- ai serve",
		"tmux new-session -d 'ai serve --role reviewer'",
		"tmux new-session -d 'AI_SUBAGENT_DEPTH=0 ai serve --role reviewer'",
		"sh -c 'AI_SUBAGENT_DEPTH=0 ai serve --role reviewer'",
		"bash -c 'nohup ai serve --role reviewer'",
	} {
		blocks, err := tool.Execute(ctx, map[string]any{"command": command})
		if err != nil {
			t.Fatalf("Execute(%q): %v", command, err)
		}
		text, ok := blocks[0].(agentctx.TextContent)
		if !ok || !strings.Contains(text.Text, "cannot launch another ai agent") {
			t.Fatalf("Execute(%q) was not blocked: %#v", command, blocks)
		}
	}
}

func TestBashToolAllowsRepeatedTopLevelAgentLaunches(t *testing.T) {
	tool := &BashTool{subagentDepth: 0}

	if tool.isSubagentLaunch("ai serve") {
		t.Fatal("top-level first launch was blocked")
	}
	if tool.isSubagentLaunch("ai run") {
		t.Fatal("top-level second launch was blocked")
	}
}

func TestBashToolDoesNotTreatOrdinaryCommandsAsAgentLaunches(t *testing.T) {
	tool := &BashTool{subagentDepth: 1}

	for _, command := range []string{
		"echo ai serve",
		"grep 'ai serve' file.txt",
		"echo 'ai serve'",
		"ai send run",
		"echo /tmp/ai serve-not-a-command",
		"env echo ai serve",
	} {
		if tool.isSubagentLaunch(command) {
			t.Fatalf("ordinary command was classified as launch: %q", command)
		}
	}
}
