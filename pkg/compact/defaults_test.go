package compact

import "testing"

func TestDefaultLLMDecideConfig(t *testing.T) {
	cases := []struct {
		window int
		name   string
	}{
		{1_000_000, "1M-class"},
		{800_000, "800K-class"},
		{200_000, "200K-class"},
		{100_000, "100K-class"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := DefaultLLMDecideConfig(c.window)
			if cfg.SoftThreshold <= 0 {
				t.Error("expected positive SoftThreshold")
			}
			if cfg.HardLimit <= 0 {
				t.Error("expected positive HardLimit")
			}
			if cfg.HardLimit <= cfg.SoftThreshold {
				t.Error("HardLimit should be > SoftThreshold")
			}
		})
	}
}
