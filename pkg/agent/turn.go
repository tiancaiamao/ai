package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

const maxContextMgmtCyclesPerTurn = 3

// ExecuteTurn runs a full user turn with automatic context management retries.
func (a *AgentNew) ExecuteTurn(ctx context.Context, userMessage string) error {
	for i := 0; i < maxContextMgmtCyclesPerTurn; i++ {
		err := a.ExecuteNormalMode(ctx, userMessage)
		if err == nil {
			return nil
		}

		var triggerErr *ContextMgmtTriggerError
		if !errors.As(err, &triggerErr) {
			return err
		}

		slog.Info("[AgentNew] Executing context management before processing user message",
			"attempt", i+1,
			"urgency", triggerErr.Urgency,
			"reason", triggerErr.Reason,
		)
		if mgmtErr := a.ExecuteContextMgmtMode(ctx, triggerErr.Urgency); mgmtErr != nil {
			return fmt.Errorf("context management failed: %w", mgmtErr)
		}
	}

	return fmt.Errorf("context management triggered repeatedly for one user message")
}
