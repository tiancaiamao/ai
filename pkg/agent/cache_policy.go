package agent

import "strings"

// CacheMode determines how runtime_state telemetry is handled in messages.
type CacheMode int

const (
	// CacheModeAuto auto-detects the mode from the model name.
	CacheModeAuto CacheMode = iota
	// CacheModeCache uses cache-first strategy: persist runtime_state in RecentMessages
	// for higher prefix cache hit rates (e.g. DeepSeek).
	CacheModeCache
	// CacheModeContext uses context-first strategy: ephemeral injection, current behavior.
	CacheModeContext
)

// RuntimeStateStrategy controls how runtime_state messages are managed.
type RuntimeStateStrategy int

const (
	// RuntimeStateEphemeral injects runtime_state temporarily per turn (context-first).
	RuntimeStateEphemeral RuntimeStateStrategy = iota
	// RuntimeStatePersist appends runtime_state as a persistent message (cache-first).
	RuntimeStatePersist
)

// MessageMutationPolicy defines how messages are mutated for a given cache strategy.
// Phase 1 minimal interface — more methods may be added in Phase 2+.
type MessageMutationPolicy interface {
	// RuntimeStateStrategy returns whether runtime_state is ephemeral or persistent.
	RuntimeStateStrategy() RuntimeStateStrategy
}

// cacheFirstPolicy implements MessageMutationPolicy for cache-first models.
type cacheFirstPolicy struct{}

func (cacheFirstPolicy) RuntimeStateStrategy() RuntimeStateStrategy {
	return RuntimeStatePersist
}

// contextFirstPolicy implements MessageMutationPolicy for context-first models.
type contextFirstPolicy struct{}

func (contextFirstPolicy) RuntimeStateStrategy() RuntimeStateStrategy {
	return RuntimeStateEphemeral
}

// DefaultMutationPolicy returns the appropriate MessageMutationPolicy for the given CacheMode.
// CacheModeAuto should be resolved via IsCacheMode before calling this function.
func DefaultMutationPolicy(mode CacheMode) MessageMutationPolicy {
	switch mode {
	case CacheModeCache:
		return cacheFirstPolicy{}
	default:
		// CacheModeContext and CacheModeAuto (fallback) both use ephemeral.
		return contextFirstPolicy{}
	}
}

// IsCacheMode detects the appropriate CacheMode from a model name.
//   - model contains "deepseek" (case-insensitive) → CacheModeCache
//   - model contains "glm" (case-insensitive) → CacheModeCache
//   - empty or anything else → CacheModeContext
func IsCacheMode(model string) CacheMode {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "deepseek") || strings.Contains(lower, "glm") {
		return CacheModeCache
	}
	return CacheModeContext
}

// ResolveCacheMode returns the effective CacheMode, resolving Auto to a concrete mode
// using the provided model name.
func ResolveCacheMode(mode CacheMode, model string) CacheMode {
	if mode == CacheModeAuto {
		return IsCacheMode(model)
	}
	return mode
}
