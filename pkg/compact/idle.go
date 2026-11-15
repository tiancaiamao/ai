package compact

import (
	"strings"
	"time"
)

// idleCompactMargin is subtracted from a provider's prompt-cache TTL so the
// idle compaction request still hits a warm prefix cache instead of paying a
// full miss on the whole conversation.
const idleCompactMargin = 5 * time.Second

// promptCacheTTL returns how long a provider keeps a prefix cache warm after
// the last request, and whether that TTL is known well enough to act on.
//
// Compacting *before* the cache expires is the whole point of idle compaction:
// the compaction request itself is a cheap cache read, and it replaces the
// full-price miss the next user turn would otherwise pay on a large context.
// Compacting after expiry is a net loss for the first turn back, so providers
// whose cache clearly survives (DeepSeek: hours to days) or whose retention is
// unknown (third-party gateways) are left alone.
func promptCacheTTL(provider string) (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		// 5-minute ephemeral prompt cache.
		return 5 * time.Minute, true
	case "openai":
		// Automatic prefix caching, evicted after roughly 5-10 idle minutes.
		return 5 * time.Minute, true
	case "zai":
		// Automatic context caching; retention is undocumented, assume the
		// same 5-minute class as the other first-party providers.
		return 5 * time.Minute, true
	}
	return 0, false
}

// IdleCompactDelay returns how long after a completed turn an idle compaction
// should run, or 0 when idle compaction is disabled for the provider.
func IdleCompactDelay(provider string) time.Duration {
	ttl, ok := promptCacheTTL(provider)
	if !ok || ttl <= idleCompactMargin {
		return 0
	}
	return ttl - idleCompactMargin
}
