package compact

import "github.com/tiancaiamao/ai/pkg/rpc"

// BuildCompactionState converts internal compactor config and state into an RPC-facing snapshot.
func BuildCompactionState(cfg *Config, compactor *Compactor) *rpc.CompactionState {
	if cfg == nil || compactor == nil {
		return nil
	}
	limit, source := compactor.EffectiveTokenLimit()
	return &rpc.CompactionState{
		MaxMessages:           cfg.MaxMessages,
		MaxTokens:             cfg.MaxTokens,
		KeepRecent:            cfg.KeepRecent,
		KeepRecentTokens:      cfg.KeepRecentTokens,
		ReserveTokens:         compactor.ReserveTokens(),
		ToolCallCutoff:        cfg.ToolCallCutoff,
		ToolSummaryStrategy:   cfg.ToolSummaryStrategy,
		ToolSummaryAutomation: cfg.ToolSummaryAutomation,
		ContextWindow:         compactor.ContextWindow(),
		TokenLimit:            limit,
		TokenLimitSource:      source,
	}
}
