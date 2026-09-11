package agent

import (
	"context"
	"encoding/json"
	"hash/fnv"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Reasons recorded on the llm_prefix_cache_check trace event. Providers can
// only reuse prompt cache for a shared token prefix, so any reason other than
// "appended"/"truncated" means the request cannot fully hit the cache left by
// the previous request.
const (
	prefixCacheColdStart     = "cold_start"            // first request of the session
	prefixCacheAppended      = "appended"              // pure tail append — full hit
	prefixCacheTruncated     = "truncated"             // shrank to a prefix of the previous request — still a hit
	prefixCacheModelChanged  = "model_changed"         // cache is per-model
	prefixCacheSystemChanged = "system_prompt_changed" // system prompt starts the cached prefix
	prefixCacheToolsChanged  = "tools_changed"         // most providers hash tools into the prefix
	prefixCacheDiverged      = "messages_diverged"     // history changed mid-sequence — the actual miss
)

// checkPrefixCache compares the fingerprint of the request about to be sent
// with the previously sent one, records the outcome on the llm_call span,
// then stores the new fingerprint. A miss is additionally recorded as an
// instant llm_prefix_cache_check trace event; hits are the common case and
// are left off the trace to avoid noise. Compaction clears the fingerprint via
// AgentState.ResetLLMRequestFingerprint so history rewrites are reported as a
// known reset rather than a divergence.
func checkPrefixCache(
	ctx context.Context,
	span *traceevent.Span,
	agentCtx *agentctx.AgentContext,
	model string,
	llmCtx llm.LLMContext,
) {
	if agentCtx == nil || agentCtx.AgentState == nil {
		return
	}
	state := agentCtx.AgentState

	prev := state.LastLLMRequest
	curr := fingerprintLLMRequest(model, llmCtx)
	reason, divergeIndex, hit := compareLLMFingerprints(prev, curr, state.PendingCacheResetReason)

	state.LastLLMRequest = curr
	state.PendingCacheResetReason = ""

	var systemChanged, toolsChanged, modelChanged bool
	prevCount := 0
	if prev != nil {
		systemChanged = prev.SystemHash != curr.SystemHash
		toolsChanged = prev.ToolsHash != curr.ToolsHash
		modelChanged = prev.Model != curr.Model
		prevCount = len(prev.MsgHashes)
	}

	span.AddField("prefix_cache_hit", hit)
	span.AddField("prefix_cache_reason", reason)
	if divergeIndex >= 0 {
		span.AddField("prefix_diverge_index", divergeIndex)
	}

	if !hit {
		traceevent.Log(ctx, traceevent.CategoryLLM, "llm_prefix_cache_check",
			traceevent.Field{Key: "hit", Value: hit},
			traceevent.Field{Key: "reason", Value: reason},
			traceevent.Field{Key: "diverge_index", Value: divergeIndex},
			traceevent.Field{Key: "prev_messages", Value: prevCount},
			traceevent.Field{Key: "curr_messages", Value: len(curr.MsgHashes)},
			traceevent.Field{Key: "system_changed", Value: systemChanged},
			traceevent.Field{Key: "tools_changed", Value: toolsChanged},
			traceevent.Field{Key: "model_changed", Value: modelChanged},
			traceevent.Field{Key: "model", Value: model},
		)
	}
}

// fingerprintLLMRequest hashes the cache-relevant parts of an outgoing
// request. Messages are hashed via their JSON encoding (see
// LLMMessage.MarshalJSON) so the fingerprint matches what providers actually
// see on the wire.
func fingerprintLLMRequest(model string, llmCtx llm.LLMContext) *agentctx.LLMRequestFingerprint {
	msgHashes := make([]uint64, len(llmCtx.Messages))
	for i, msg := range llmCtx.Messages {
		msgHashes[i] = hashJSON(msg)
	}
	return &agentctx.LLMRequestFingerprint{
		Model:      model,
		SystemHash: hashString(llmCtx.SystemPrompt),
		ToolsHash:  hashJSON(llmCtx.Tools),
		MsgHashes:  msgHashes,
	}
}

// compareLLMFingerprints reports prefix-cache reuse between two consecutive
// requests. It returns the reason label, the message index at which the
// previous request's prefix diverges (-1 when not applicable), and whether
// the request should fully hit the provider cache.
func compareLLMFingerprints(prev, curr *agentctx.LLMRequestFingerprint, pendingReset string) (reason string, divergeIndex int, hit bool) {
	switch {
	case prev == nil:
		if pendingReset != "" {
			return pendingReset, -1, false
		}
		return prefixCacheColdStart, -1, false
	case prev.Model != curr.Model:
		return prefixCacheModelChanged, -1, false
	case prev.SystemHash != curr.SystemHash:
		return prefixCacheSystemChanged, -1, false
	case prev.ToolsHash != curr.ToolsHash:
		return prefixCacheToolsChanged, -1, false
	}

	lcp := 0
	for lcp < len(prev.MsgHashes) && lcp < len(curr.MsgHashes) && prev.MsgHashes[lcp] == curr.MsgHashes[lcp] {
		lcp++
	}
	switch {
	case lcp == len(prev.MsgHashes):
		// Everything sent last time is still there, only appended to.
		return prefixCacheAppended, -1, true
	case lcp == len(curr.MsgHashes):
		// Request shrank to a strict prefix of the previous one; every token
		// was part of the cached sequence, so the provider still hits.
		return prefixCacheTruncated, -1, true
	default:
		return prefixCacheDiverged, lcp, false
	}
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// hashJSON hashes the JSON encoding of v. Values that cannot marshal (not
// expected for LLM messages and tool schemas) fall back to a stable marker so
// the fingerprint stays deterministic within a process.
func hashJSON(v any) uint64 {
	data, err := json.Marshal(v)
	if err != nil {
		return hashString("!unmarshalable")
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}
