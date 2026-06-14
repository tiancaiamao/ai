package context

import "testing"

// TestAgentState_TokensSinceLastDeltaCompaction verifies the new field is
// initialized to 0 and survives Clone.
func TestAgentState_TokensSinceLastDeltaCompaction(t *testing.T) {
	s := NewAgentState("sess", "/tmp")
	if s.TokensSinceLastDeltaCompaction != 0 {
		t.Fatalf("expected initial TokensSinceLastDeltaCompaction=0, got %d", s.TokensSinceLastDeltaCompaction)
	}

	s.TokensSinceLastDeltaCompaction = 4242
	clone := s.Clone()
	if clone.TokensSinceLastDeltaCompaction != 4242 {
		t.Fatalf("clone TokensSinceLastDeltaCompaction = %d, want 4242", clone.TokensSinceLastDeltaCompaction)
	}

	// Mutating the clone must not affect the original.
	clone.TokensSinceLastDeltaCompaction = 0
	if s.TokensSinceLastDeltaCompaction != 4242 {
		t.Fatalf("original changed after mutating clone: got %d", s.TokensSinceLastDeltaCompaction)
	}
}
