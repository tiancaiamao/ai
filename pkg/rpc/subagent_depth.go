package rpc

import (
	"fmt"
	"os"
	"strconv"
	"sync"
)

const subagentDepthEnv = "AI_SUBAGENT_DEPTH"

var (
	subagentDepthOnce sync.Once
	subagentDepthErr  error
)

// advanceSubagentDepth enforces the two-level agent nesting limit. The
// launcher passes the environment through unchanged; each agent process
// advances the marker exactly once when its shared RPC/ACP setup begins.
func advanceSubagentDepth(lookupEnv func(string) (string, bool), setenv func(string, string) error) error {
	value, set := lookupEnv(subagentDepthEnv)
	depth := 0
	if set {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return fmt.Errorf("invalid AI_SUBAGENT_DEPTH=%q; start a new top-level agent", value)
		}
		depth = parsed + 1
	}
	if depth >= 2 {
		return fmt.Errorf("nested agent limit reached: an agent may create at most one subagent")
	}
	return setenv(subagentDepthEnv, strconv.Itoa(depth))
}

func advanceProcessSubagentDepth() error {
	subagentDepthOnce.Do(func() {
		subagentDepthErr = advanceSubagentDepth(os.LookupEnv, os.Setenv)
	})
	return subagentDepthErr
}
