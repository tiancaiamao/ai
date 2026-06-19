package agent

import "testing"

func TestSafeRatio(t *testing.T) {
	tests := []struct {
		num, den int64
		want     float64
	}{
		{10, 2, 5.0},
		{0, 5, 0.0},
		{5, 0, 0.0},
		{5, -1, 0.0},
	}
	for _, tt := range tests {
		got := safeRatio(tt.num, tt.den)
		if got != tt.want {
			t.Errorf("safeRatio(%d, %d) = %f, want %f", tt.num, tt.den, got, tt.want)
		}
	}
}

func TestSafeDurationMillis(t *testing.T) {
	tests := []struct {
		totalNs int64
		calls   int64
		want    int64
	}{
		{1_000_000_000, 1, 1000}, // 1s = 1000ms
		{2_000_000_000, 2, 1000}, // avg 1s = 1000ms
		{0, 1, 0},
		{1000, 0, 0},
		{1000, -1, 0},
	}
	for _, tt := range tests {
		got := safeDurationMillis(tt.totalNs, tt.calls)
		if got != tt.want {
			t.Errorf("safeDurationMillis(%d, %d) = %d, want %d", tt.totalNs, tt.calls, got, tt.want)
		}
	}
}
