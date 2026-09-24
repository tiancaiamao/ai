package compact

// CompactionState represents compaction thresholds and settings.
type CompactionState struct {
	MaxTokens             int    `json:"maxTokens,omitempty"`
	KeepRecentTokens      int    `json:"keepRecentTokens,omitempty"`
	ReserveTokens         int    `json:"reserveTokens,omitempty"`
	ToolCallCutoff        int    `json:"toolCallCutoff,omitempty"`
	ToolSummaryAutomation string `json:"toolSummaryAutomation,omitempty"`
	ContextWindow         int    `json:"contextWindow,omitempty"`
	TokenLimit            int    `json:"tokenLimit,omitempty"`
	TokenLimitSource      string `json:"tokenLimitSource,omitempty"`
}

// BuildCompactionState converts internal compactor config and state into an RPC-facing snapshot.
func BuildCompactionState(cfg *Config, compactor *Compactor) *CompactionState {
	if cfg == nil || compactor == nil {
		return nil
	}
	limit, source := compactor.EffectiveTokenLimit()
	return &CompactionState{
		MaxTokens:             cfg.MaxTokens,
		KeepRecentTokens:      cfg.KeepRecentTokens,
		ReserveTokens:         compactor.ReserveTokens(),
		ToolCallCutoff:        cfg.ToolCallCutoff,
		ToolSummaryAutomation: cfg.ToolSummaryAutomation,
		ContextWindow:         compactor.ContextWindow(),
		TokenLimit:            limit,
		TokenLimitSource:      source,
	}
}
