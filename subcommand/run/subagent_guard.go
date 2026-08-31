package run

import (
	"fmt"
	"strconv"
	"strings"
)

const subagentDepthEnv = "AI_SUBAGENT_DEPTH"

// checkSubagentSpawnAllowed prevents a child agent from creating another
// agent subprocess. The marker is inherited by commands executed by the RPC
// child, including nested ai run/serve invocations.
func checkSubagentSpawnAllowed(getenv func(string) string) error {
	value := strings.TrimSpace(getenv(subagentDepthEnv))
	if value == "" {
		return nil
	}
	depth, err := strconv.Atoi(value)
	if err != nil || depth < 0 || depth > 0 {
		return fmt.Errorf("blocked: subagents cannot create nested subagents")
	}
	return nil
}

// subagentProcessEnv marks the RPC child one level deeper than its launcher.
// An unset marker denotes the top-level launcher and therefore produces a
// top-level agent at depth zero.
func subagentProcessEnv(parent []string) []string {
	depth := 0
	for _, entry := range parent {
		if strings.HasPrefix(entry, subagentDepthEnv+"=") {
			value := strings.TrimPrefix(entry, subagentDepthEnv+"=")
			if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
				depth = parsed + 1
			}
			break
		}
	}

	marker := fmt.Sprintf("%s=%d", subagentDepthEnv, depth)
	env := make([]string, 0, len(parent)+1)
	found := false
	for _, entry := range parent {
		if strings.HasPrefix(entry, subagentDepthEnv+"=") {
			if !found {
				env = append(env, marker)
				found = true
			}
			continue
		}
		env = append(env, entry)
	}
	if !found {
		env = append(env, marker)
	}
	return env
}
