package app

import (
	"context"
	"time"

	"github.com/tiancaiamao/ai/pkg/protocol"
)

// RunACP initializes the application runtime and serves ACP on conn.
func RunACP(conn protocol.Conn, sessionPath, debugAddr, customSystemPrompt string, maxTurns int, timeout time.Duration, role, modelOverride, runID string) error {
	return RunACPWithContext(context.Background(), conn, sessionPath, debugAddr, customSystemPrompt, maxTurns, timeout, role, modelOverride, runID)
}

// RunACPWithContext initializes the application runtime and serves ACP until
// the transport closes or ctx is canceled.
func RunACPWithContext(ctx context.Context, conn protocol.Conn, sessionPath, debugAddr, customSystemPrompt string, maxTurns int, timeout time.Duration, role, modelOverride, runID string) error {
	return runACP(ctx, conn, sessionPath, debugAddr, customSystemPrompt, maxTurns, timeout, role, "", modelOverride, runID)
}

// RunACPWithAgentConfigContext serves ACP with an explicit agent.yaml.
func RunACPWithAgentConfigContext(ctx context.Context, conn protocol.Conn, sessionPath, debugAddr, customSystemPrompt string, maxTurns int, timeout time.Duration, agentConfigPath, modelOverride, runID string) error {
	return runACP(ctx, conn, sessionPath, debugAddr, customSystemPrompt, maxTurns, timeout, "", agentConfigPath, modelOverride, runID)
}

func runACP(ctx context.Context, conn protocol.Conn, sessionPath, debugAddr, customSystemPrompt string, maxTurns int, timeout time.Duration, role, agentConfigPath, modelOverride, runID string) error {
	runtime, err := NewApp(sessionPath, AppSetupParams{
		CustomSystemPrompt: customSystemPrompt,
		MaxTurns:           maxTurns,
		DebugAddr:          debugAddr,
		Role:               role,
		AgentConfigPath:    agentConfigPath,
		ModelOverride:      modelOverride,
		RunID:              runID,
	})
	if err != nil {
		return err
	}
	ag, closeWriter, err := runtime.SetupAgent(maxTurns)
	if err != nil {
		return err
	}
	defer closeWriter()
	defer ag.Shutdown()
	return protocol.Run(ctx, conn, runtime, timeout)
}
