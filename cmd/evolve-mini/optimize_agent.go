package main

import (
	"context"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

// OptimizeAgent generates code improvements based on evolution feedback.
type OptimizeAgent struct {
	// In production, this would include LLM client
}

// NewOptimizeAgent creates a new optimization agent.
func NewOptimizeAgent() *OptimizeAgent {
	return &OptimizeAgent{}
}

// GenerateMutation generates a code mutation based on evolution history.
func (oa *OptimizeAgent) GenerateMutation(ctx context.Context, history *EvolutionHistory) (*Mutation, error) {
	// Stub: In production, this would call LLM to analyze scores and generate code changes

	if history.BestScore == nil {
		return &Mutation{
			Analysis:    "No baseline score yet, returning empty mutation",
			FileChanges: map[string]string{},
		}, nil
	}

	return &Mutation{
		Analysis:    "Phase 4 stub: returning empty mutation for testing",
		FileChanges: map[string]string{},
	}, nil
}

// EvolutionHistory wraps evolvemini.GenerationRecord slice with helpers.
type EvolutionHistory struct {
	CurrentGeneration int
	BestScore        *evolvemini.SuiteScore
	AllGenerations  []evolvemini.GenerationRecord
}

// GetWorstCases returns N lowest-scoring cases from best generation.
func (eh *EvolutionHistory) GetWorstCases(n int) []evolvemini.CaseScore {
	if eh.BestScore == nil || len(eh.BestScore.CaseScores) == 0 {
		return nil
	}

	scores := make([]evolvemini.CaseScore, len(eh.BestScore.CaseScores))
	copy(scores, eh.BestScore.CaseScores)

	if n > len(scores) {
		n = len(scores)
	}
	return scores[:n]
}

// GetBestCases returns N highest-scoring cases from best generation.
func (eh *EvolutionHistory) GetBestCases(n int) []evolvemini.CaseScore {
	if eh.BestScore == nil || len(eh.BestScore.CaseScores) == 0 {
		return nil
	}

	scores := make([]evolvemini.CaseScore, len(eh.BestScore.CaseScores))
	copy(scores, eh.BestScore.CaseScores)

	if n > len(scores) {
		n = len(scores)
	}
	return scores[:n]
}

// Mutation represents a code change.
type Mutation struct {
	Analysis    string              `json:"analysis"`
	FileChanges map[string]string `json:"fileChanges"` // filepath -> new content
}