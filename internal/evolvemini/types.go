package evolvemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Snapshot is a frozen AgentContext state extracted from a real session.
type Snapshot struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Tags        []string `json:"tags"`

	RecentMessages []agentctx.AgentMessage `json:"recentMessages"`
	LLMContext     string                  `json:"llmContext"`
	AgentState     *agentctx.AgentState    `json:"agentState"`
	ContextWindow  int                     `json:"contextWindow"`

	FollowUpTask   string `json:"followUpTask"`
	FollowUpAnswer string `json:"followUpAnswer"`

	SourceSession string `json:"sourceSession"`
	ExtractedAt   string `json:"extractedAt"`
}

// SnapshotSuite is a collection of diverse snapshots.
type SnapshotSuite struct {
	Snapshots []Snapshot `json:"snapshots"`
}

func LoadSuite(dir string) (*SnapshotSuite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read suite dir: %w", err)
	}
	suite := &SnapshotSuite{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		suite.Snapshots = append(suite.Snapshots, snap)
	}
	return suite, nil
}

func (s *SnapshotSuite) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, snap := range s.Snapshots {
		name := fmt.Sprintf("%03d_%s.json", i, snap.ID)
		data, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WorkerInput is sent to the worker binary via stdin JSON.
type WorkerInput struct {
	Snapshot Snapshot `json:"snapshot"`
}

// WorkerOutput is read from the worker binary via stdout JSON.
type WorkerOutput struct {
	Success bool `json:"success"`
	Error   string `json:"error,omitempty"`

	TokensBefore int `json:"tokensBefore"`
	TokensAfter  int `json:"tokensAfter"`

	// Tool calls inferred from context changes
	InferredToolAction string `json:"inferredToolAction"` // "truncate_messages" | "update_llm_context" | "no_action" | "unknown"

	ContextBefore   string `json:"contextBefore"`
	ContextAfter    string `json:"contextAfter"`
	LLMContextBefore string `json:"llmContextBefore"`
	LLMContextAfter string `json:"llmContextAfter"`
}

// ToolCallRecord captures a single tool invocation during compact.
type ToolCallRecord struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result string         `json:"result"`
	Error  string         `json:"error,omitempty"`
}

// CaseScore is the judge's verdict for a single snapshot.
type CaseScore struct {
	SnapshotID string `json:"snapshotId"`
	Scores     struct {
		InfoRetention      int `json:"infoRetention"`
		TaskExecutability  int `json:"taskExecutability"`
		DecisionCorrectness int `json:"decisionCorrectness"`
		ContextAccuracy    int `json:"contextAccuracy"`
		TokenEfficiency    int `json:"tokenEfficiency"`
	} `json:"scores"`
	WeightedTotal     int    `json:"weightedTotal"`
	OverallAssessment string `json:"overallAssessment"`
	JudgeRaw          string `json:"judgeRaw,omitempty"`
}

// SuiteScore is the aggregated score across all snapshots in a suite.
type SuiteScore struct {
	Generation       int                   `json:"generation"`
	WeightedAverage  float64               `json:"weightedAverage"`
	DimensionScores  map[string]float64    `json:"dimensionScores"`
	CaseScores       []CaseScore           `json:"caseScores"`
	WorstCaseIDs     []string              `json:"worstCaseIds"`
	BestCaseIDs      []string              `json:"bestCaseIds"`
	StdDev           float64               `json:"stdDev"`
	MinScore         float64               `json:"minScore"`
	DeltaVsBaseline  float64               `json:"deltaVsBaseline"`
	ImprovedCases    int                   `json:"improvedCases"`
	RegressedCases   int                   `json:"regressedCases"`
}

// GenerationRecord stores everything about one evolution step.
type GenerationRecord struct {
	Generation   int    `json:"generation"`
	ParentGen    int    `json:"parentGen"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`

	OptimizeRationale string `json:"optimizeRationale,omitempty"`
	DiffPatch         string `json:"diffPatch,omitempty"`

	SuiteScore *SuiteScore `json:"suiteScore,omitempty"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}