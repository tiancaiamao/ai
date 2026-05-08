package cmd

import (
	"path/filepath"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

// DetectStale checks whether a legacy bridge agent is stale.
// AI-backed runs are managed by the ai runtime and are skipped here.
func DetectStale(id string, act *agent.Activity) {
	if act.Status != "running" {
		return
	}

	if act.Backend == "ai" {
		return
	}

	stale := false
	reason := ""

	// Check process liveness via signal 0.
		if act.Pid > 0 {
		if !agent.IsProcessAlive(act.Pid) {
			stale = true
			reason = "process no longer alive"
		}
	} else {
		agentDir := agent.AgentDir(id)
		sockPath := filepath.Join(agentDir, "bridge.sock")
		if !storage.Exists(sockPath) {
			stale = true
			reason = "no PID and no bridge socket"
		}
	}

	if stale {
		act.Status = "failed"
		if act.Error == "" {
			act.Error = reason
		}
		now := time.Now().Unix()
		if act.FinishedAt == 0 {
			act.FinishedAt = now
		}
		agentDir := agent.AgentDir(id)
		_ = storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act)
	}
}
