package main

import "github.com/tiancaiamao/ai/internal/evolvemini"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"github.com/tiancaiamao/ai/pkg/config"
)

// runScore executes a worker binary against a snapshot suite, then uses
// LLM-as-judge to score each case, and aggregates into a evolvemini.SuiteScore.
func runScore(workerBinary, suiteDir string) error {
	fmt.Printf("Loading suite from %s ...\n", suiteDir)
	suite, err := evolvemini.LoadSuite(suiteDir)
	if err != nil {
		return fmt.Errorf("load suite: %w", err)
	}
	if len(suite.Snapshots) == 0 {
		return fmt.Errorf("no snapshots found in %s", suiteDir)
	}
	fmt.Printf("Loaded %d snapshots\n", len(suite.Snapshots))

	var caseScores []evolvemini.CaseScore
	for i, snap := range suite.Snapshots {
		fmt.Printf("\n[%d/%d] Scoring %s ...\n", i+1, len(suite.Snapshots), snap.ID)

		output, err := runWorker(workerBinary, snap)
		if err != nil {
			fmt.Printf("  Worker failed: %v\n", err)
			// Create a failure score with all 1s
			cs := evolvemini.CaseScore{
				SnapshotID:        snap.ID,
				WeightedTotal:     1*3 + 1*3 + 1*2 + 1*2 + 1*1, // = 11
				OverallAssessment: fmt.Sprintf("worker error: %v", err),
			}
			cs.Scores.InfoRetention = 1
			cs.Scores.TaskExecutability = 1
			cs.Scores.DecisionCorrectness = 1
			cs.Scores.ContextAccuracy = 1
			cs.Scores.TokenEfficiency = 1
			caseScores = append(caseScores, cs)
			continue
		}

		fmt.Printf("  Worker succeeded: %d -> %d tokens (saved %d)\n",
			output.TokensBefore, output.TokensAfter, output.TokensBefore-output.TokensAfter)

		cs, err := judgeLLM(snap, *output)
		if err != nil {
			fmt.Printf("  Judge failed: %v\n", err)
			cs = evolvemini.CaseScore{
				SnapshotID:        snap.ID,
				WeightedTotal:     11,
				OverallAssessment: fmt.Sprintf("judge error: %v", err),
			}
			cs.Scores.InfoRetention = 1
			cs.Scores.TaskExecutability = 1
			cs.Scores.DecisionCorrectness = 1
			cs.Scores.ContextAccuracy = 1
			cs.Scores.TokenEfficiency = 1
		}

		caseScores = append(caseScores, cs)
		fmt.Printf("  Score: %d (%s)\n", cs.WeightedTotal, cs.OverallAssessment)
	}

	suiteScore := aggregateScores(caseScores, 0)

	// Print summary table
	printScoreTable(suiteScore)

	// Save to suiteDir/score.json
	scorePath := filepath.Join(suiteDir, "score.json")
	data, err := json.MarshalIndent(suiteScore, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suite score: %w", err)
	}
	if err := os.WriteFile(scorePath, data, 0o644); err != nil {
		return fmt.Errorf("write score.json: %w", err)
	}
	fmt.Printf("\nSuite score saved to %s\n", scorePath)

	return nil
}

// runWorker executes the worker binary for a single snapshot and returns its output.
func runWorker(workerBinary string, snap evolvemini.Snapshot) (*evolvemini.WorkerOutput, error) {
	input := evolvemini.WorkerInput{Snapshot: snap}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal worker input: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, workerBinary)
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("worker binary failed: %w\nstderr: %s", err, stderr.String())
	}

	var output evolvemini.WorkerOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, fmt.Errorf("parse worker output: %w\nstdout: %s\nstderr: %s",
			err, stdout.String(), stderr.String())
	}

	if !output.Success {
		return nil, fmt.Errorf("worker reported failure: %s", output.Error)
	}

	return &output, nil
}

// judgeLLM calls an LLM to evaluate the quality of a compact operation.
// It returns a evolvemini.CaseScore with per-dimension scores and overall assessment.
func judgeLLM(snapshot evolvemini.Snapshot, output evolvemini.WorkerOutput) (evolvemini.CaseScore, error) {
	// Use default model: zai/glm-4.7
	provider := "zai"
	modelID := "glm-4.7"

	// Resolve API key
	apiKey, err := config.ResolveAPIKey(provider)
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("resolve judge API key: %w", err)
	}

	// Resolve model spec
	modelSpec, err := resolveModelSpec(provider, modelID)
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("resolve judge model: %w", err)
	}

	baseURL := modelSpec.BaseURL

	// Build judge prompt
	truncateStr := func(s string, maxLen int) string {
		if len(s) <= maxLen {
			return s
		}
		return s[:maxLen] + "\n... [truncated]"
	}

	contextBefore := truncateStr(output.ContextBefore, 8000)
	contextAfter := truncateStr(output.ContextAfter, 8000)

	// Format inferred tool action
	var toolCallsStr string
	if output.InferredToolAction == "truncate_messages" || output.InferredToolAction == "update_llm_context" {
		toolCallsStr = output.InferredToolAction
	} else if output.InferredToolAction == "no_action" {
		toolCallsStr = "(no action taken)"
	} else {
		toolCallsStr = fmt.Sprintf("(inferred: %s)", output.InferredToolAction)
	}

	prompt := fmt.Sprintf(`你是上下文管理质量评审专家。

## 场景
一个 AI coding agent 在执行任务过程中，上下文变得很大。
系统触发了一次 mini compact 操作来优化上下文。

## 评审材料

### 原始上下文（compact 前，%d tokens）
%s

### compact 后的上下文（%d tokens，节省 %d tokens）
%s

### 新的 LLM Context
%s

### 执行的工具调用
%s

### 后续任务
用户接下来会问：%s
正确执行需要知道：%s

## 评分维度（每项 1-5 分）

1. **信息保留** (权重 3x): 关键信息（文件路径、函数名、决策、约束）是否完整保留？
2. **任务可执行性** (权重 3x): 基于compact后的上下文能否正确执行后续任务？会丢失哪些关键信息？
3. **决策正确性** (权重 2x): truncate选择的ID是否正确？该截的截了、不该截的没截？
4. **上下文准确性** (权重 2x): LLMContext是否准确反映当前任务状态？
5. **token效率** (权重 1x): token节省是否显著且值得？

输出严格的 JSON:
{"infoRetention":N,"taskExecutability":N,"decisionCorrectness":N,"contextAccuracy":N,"tokenEfficiency":N,"overallAssessment":"brief summary"}`,
		output.TokensBefore, contextBefore,
		output.TokensAfter, output.TokensBefore-output.TokensAfter, contextAfter,
		output.LLMContextAfter,
		toolCallsStr,
		snapshot.FollowUpTask,
		snapshot.FollowUpAnswer,
	)

	// Build request body (non-streaming)
	reqBody := map[string]any{
		"model": modelID,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream": false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("marshal judge request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := newHTTPRequest(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("create judge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("judge API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp.Body)
		return evolvemini.CaseScore{}, fmt.Errorf("judge API returned %d: %s", resp.StatusCode, string(body))
	}

	var respData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("decode judge response: %w", err)
	}

	if len(respData.Choices) == 0 {
		return evolvemini.CaseScore{}, fmt.Errorf("judge returned no choices")
	}

	content := respData.Choices[0].Message.Content

	// Parse the JSON from the LLM response
	return parseJudgeResponse(snapshot.ID, content)
}

// parseJudgeResponse extracts the evolvemini.CaseScore from the LLM judge's response content.
func parseJudgeResponse(snapshotID, content string) (evolvemini.CaseScore, error) {
	// Try direct JSON parse first
	var result struct {
		InfoRetention       int    `json:"infoRetention"`
		TaskExecutability   int    `json:"taskExecutability"`
		DecisionCorrectness int    `json:"decisionCorrectness"`
		ContextAccuracy     int    `json:"contextAccuracy"`
		TokenEfficiency     int    `json:"tokenEfficiency"`
		OverallAssessment   string `json:"overallAssessment"`
	}

	err := json.Unmarshal([]byte(content), &result)
	if err != nil {
		// Try to extract JSON from markdown code blocks
		re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(\\{.*?\\})\\s*\\n?```")
		matches := re.FindStringSubmatch(content)
		if len(matches) >= 2 {
			err = json.Unmarshal([]byte(matches[1]), &result)
		}
	}
	if err != nil {
		// Last resort: try to find any JSON object in the text
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			err = json.Unmarshal([]byte(content[start:end+1]), &result)
		}
	}
	if err != nil {
		return evolvemini.CaseScore{}, fmt.Errorf("parse judge JSON: %w\nraw: %s", err, content)
	}

	// Clamp scores to 1-5
	clamp := func(v int) int {
		if v < 1 {
			return 1
		}
		if v > 5 {
			return 5
		}
		return v
	}

	cs := evolvemini.CaseScore{
		SnapshotID:        snapshotID,
		WeightedTotal:     clamp(result.InfoRetention)*3 + clamp(result.TaskExecutability)*3 + clamp(result.DecisionCorrectness)*2 + clamp(result.ContextAccuracy)*2 + clamp(result.TokenEfficiency)*1,
		OverallAssessment: result.OverallAssessment,
		JudgeRaw:          content,
	}
	cs.Scores.InfoRetention = clamp(result.InfoRetention)
	cs.Scores.TaskExecutability = clamp(result.TaskExecutability)
	cs.Scores.DecisionCorrectness = clamp(result.DecisionCorrectness)
	cs.Scores.ContextAccuracy = clamp(result.ContextAccuracy)
	cs.Scores.TokenEfficiency = clamp(result.TokenEfficiency)

	return cs, nil
}

// aggregateScores combines CaseScores into a evolvemini.SuiteScore.

// printScoreTable prints a formatted summary table of all case scores.
// aggregateScores combines CaseScores into a evolvemini.SuiteScore.
func aggregateScores(scores []evolvemini.CaseScore, generation int) evolvemini.SuiteScore {
	n := len(scores)
	if n == 0 {
		return evolvemini.SuiteScore{
			Generation:      generation,
			DimensionScores: make(map[string]float64),
		}
	}

	var totalWeighted float64
	dimSums := map[string]float64{
		"infoRetention":       0,
		"taskExecutability":   0,
		"decisionCorrectness": 0,
		"contextAccuracy":     0,
		"tokenEfficiency":     0,
	}

	for _, cs := range scores {
		totalWeighted += float64(cs.WeightedTotal)
		dimSums["infoRetention"] += float64(cs.Scores.InfoRetention)
		dimSums["taskExecutability"] += float64(cs.Scores.TaskExecutability)
		dimSums["decisionCorrectness"] += float64(cs.Scores.DecisionCorrectness)
		dimSums["contextAccuracy"] += float64(cs.Scores.ContextAccuracy)
		dimSums["tokenEfficiency"] += float64(cs.Scores.TokenEfficiency)
	}

	avgWeighted := totalWeighted / float64(n)

	dimAvgs := make(map[string]float64)
	for k, v := range dimSums {
		dimAvgs[k] = v / float64(n)
	}

	var sumSqDiff float64
	for _, cs := range scores {
		diff := float64(cs.WeightedTotal) - avgWeighted
		sumSqDiff += diff * diff
	}
	stdDev := math.Sqrt(sumSqDiff / float64(n))

	sorted := make([]evolvemini.CaseScore, n)
	copy(sorted, scores)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].WeightedTotal < sorted[j].WeightedTotal
	})

	minScore := float64(sorted[0].WeightedTotal)

	worstN := 3
	if n < worstN {
		worstN = n
	}
	worstIDs := make([]string, worstN)
	for i := 0; i < worstN; i++ {
		worstIDs[i] = sorted[i].SnapshotID
	}

	bestN := 3
	if n < bestN {
		bestN = n
	}
	bestIDs := make([]string, bestN)
	for i := 0; i < bestN; i++ {
		bestIDs[i] = sorted[n-1-i].SnapshotID
	}

	return evolvemini.SuiteScore{
		Generation:      generation,
		WeightedAverage: avgWeighted,
		DimensionScores: dimAvgs,
		CaseScores:      scores,
		WorstCaseIDs:    worstIDs,
		BestCaseIDs:     bestIDs,
		StdDev:          stdDev,
		MinScore:        minScore,
	}
}

func printScoreTable(ss evolvemini.SuiteScore) {
	fmt.Println()
	fmt.Printf("%-16s | %-6s | %-4s | %-8s | %-8s | %-9s | %-5s\n",
		"Snapshot", "Retain", "Exec", "Decision", "Accuracy", "Efficiency", "Total")
	fmt.Printf("%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s\n",
		strings.Repeat("-", 16), strings.Repeat("-", 6), strings.Repeat("-", 4),
		strings.Repeat("-", 8), strings.Repeat("-", 8), strings.Repeat("-", 9), strings.Repeat("-", 5))

	for _, cs := range ss.CaseScores {
		fmt.Printf("%-16s |   %d    |  %d   |    %d     |    %d     |     %d      |  %d\n",
			cs.SnapshotID,
			cs.Scores.InfoRetention,
			cs.Scores.TaskExecutability,
			cs.Scores.DecisionCorrectness,
			cs.Scores.ContextAccuracy,
			cs.Scores.TokenEfficiency,
			cs.WeightedTotal,
		)
	}

	maxScore := 0.0
	if len(ss.CaseScores) > 0 {
		for _, cs := range ss.CaseScores {
			v := float64(cs.WeightedTotal)
			if v > maxScore {
				maxScore = v
			}
		}
	}

	fmt.Printf("Average: %.1f | StdDev: %.1f | Min: %.0f | Max: %.0f\n",
		ss.WeightedAverage, ss.StdDev, ss.MinScore, maxScore)
}

// newHTTPRequest is a helper to create an HTTP request (wraps http.NewRequestWithContext).
func newHTTPRequest(ctx context.Context, method, url string, body *bytes.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, body)
}

// readAll reads all bytes from a reader (wraps io.ReadAll).
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r)
	return buf.Bytes(), err
}
