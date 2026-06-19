package compact

import "testing"

func TestBuildCompactionState(t *testing.T) {
	t.Run("nil cfg", func(t *testing.T) {
		if got := BuildCompactionState(nil, &Compactor{}); got != nil {
			t.Errorf("expected nil for nil cfg")
		}
	})

	t.Run("nil compactor", func(t *testing.T) {
		if got := BuildCompactionState(&Config{}, nil); got != nil {
			t.Errorf("expected nil for nil compactor")
		}
	})

	t.Run("both nil", func(t *testing.T) {
		if got := BuildCompactionState(nil, nil); got != nil {
			t.Errorf("expected nil")
		}
	})
}
