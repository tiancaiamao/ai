package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/tiancaiamao/ai/pkg/traceevent"
)

const maxContextMgmtCyclesPerTurn = 3

// ExecuteTurn runs a full user turn with automatic context management retries.
func (a *AgentNew) ExecuteTurn(ctx context.Context, userMessage string) error {
	promptSpan := traceevent.StartSpan(ctx, "prompt", traceevent.CategoryEvent,
		traceevent.Field{Key: "message", Value: userMessage},
	)
	defer promptSpan.End()
	ctx = promptSpan.Context()

	eventLoopSpan := promptSpan.StartChild("event_loop",
		traceevent.Field{Key: "max_context_mgmt_cycles", Value: maxContextMgmtCyclesPerTurn},
	)
	defer eventLoopSpan.End()
	ctx = eventLoopSpan.Context()

	for i := 0; i < maxContextMgmtCyclesPerTurn; i++ {
		if i == 0 {
			traceevent.Log(ctx, traceevent.CategoryEvent, "turn_start",
				traceevent.Field{Key: "source", Value: "execute_turn"},
			)
		}

		err := a.ExecuteNormalMode(ctx, userMessage)
		if err == nil {
			traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
				traceevent.Field{Key: "status", Value: "success"},
				traceevent.Field{Key: "context_mgmt_cycles", Value: i},
			)
			return nil
		}

		var triggerErr *ContextMgmtTriggerError
		if !errors.As(err, &triggerErr) {
			traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
				traceevent.Field{Key: "status", Value: "error"},
				traceevent.Field{Key: "error", Value: err.Error()},
			)
			return err
		}

		traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
			traceevent.Field{Key: "action", Value: "execute"},
			traceevent.Field{Key: "urgency", Value: triggerErr.Urgency},
			traceevent.Field{Key: "reason", Value: triggerErr.Reason},
			traceevent.Field{Key: "attempt", Value: i + 1},
		)

		slog.Info("[AgentNew] Executing context management before processing user message",
			"attempt", i+1,
			"urgency", triggerErr.Urgency,
			"reason", triggerErr.Reason,
		)
		if mgmtErr := a.ExecuteContextMgmtMode(ctx, triggerErr.Urgency); mgmtErr != nil {
			traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
				traceevent.Field{Key: "status", Value: "error"},
				traceevent.Field{Key: "error", Value: mgmtErr.Error()},
			)
			return fmt.Errorf("context management failed: %w", mgmtErr)
		}
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_skipped",
		traceevent.Field{Key: "reason", Value: "max_context_mgmt_cycles_reached"},
		traceevent.Field{Key: "max_cycles", Value: maxContextMgmtCyclesPerTurn},
	)
	traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
		traceevent.Field{Key: "status", Value: "error"},
		traceevent.Field{Key: "error", Value: "context management triggered repeatedly"},
	)

	return fmt.Errorf("context management triggered repeatedly for one user message")
}
