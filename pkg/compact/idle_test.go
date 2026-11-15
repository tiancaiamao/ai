package compact

import (
	"testing"
	"time"
)

func TestIdleCompactDelay(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     time.Duration
	}{
		{"anthropic", "anthropic", 4*time.Minute + 55*time.Second},
		{"openai", "openai", 4*time.Minute + 55*time.Second},
		{"zai", "zai", 4*time.Minute + 55*time.Second},
		{"case and spaces", " Anthropic ", 4*time.Minute + 55*time.Second},
		// DeepSeek keeps its cache for hours: compacting at 5 minutes would
		// burn tokens while the cache is still warm.
		{"deepseek disabled", "deepseek", 0},
		{"gateway disabled", "opencode", 0},
		{"unknown disabled", "", 0},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if got := IdleCompactDelay(c.provider); got != c.want {
				t.Errorf("IdleCompactDelay(%q) = %v, want %v", c.provider, got, c.want)
			}
		})
	}
}

func TestIdleCompactDelayStaysInsideTTL(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai", "zai"} {
		ttl, ok := promptCacheTTL(provider)
		if !ok {
			t.Fatalf("promptCacheTTL(%q) should be known", provider)
		}
		if got := IdleCompactDelay(provider); got >= ttl {
			t.Errorf("IdleCompactDelay(%q) = %v, must be shorter than the %v TTL", provider, got, ttl)
		}
	}
}
