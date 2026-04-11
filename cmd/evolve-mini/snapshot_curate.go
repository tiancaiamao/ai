package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

type caseEvidenceRecord struct {
	SnapshotID string `json:"snapshot_id"`
	Worker     *struct {
		Success      bool `json:"success"`
		TokensBefore int  `json:"tokensBefore"`
		TokensAfter  int  `json:"tokensAfter"`
	} `json:"worker"`
	Score *struct {
		SnapshotID        string `json:"snapshotId"`
		WeightedTotal     int    `json:"weightedTotal"`
		OverallAssessment string `json:"overallAssessment"`
	} `json:"score"`
}

type snapshotEvidenceStats struct {
	Observations int
	Failures     int
	WorkerErrors int
	JudgeErrors  int
	NonSaving    int
}

type curationDecision struct {
	Snapshot  evolvemini.Snapshot
	Drop      bool
	Reason    string
	RiskScore int
}

func runSnapshotCurate(suiteDir, outputDir, evidenceDir string) error {
	suite, err := evolvemini.LoadSuite(suiteDir)
	if err != nil {
		return fmt.Errorf("load suite: %w", err)
	}
	if len(suite.Snapshots) == 0 {
		return fmt.Errorf("no snapshots found in %s", suiteDir)
	}

	statsBySnapshot, err := collectEvidenceStats(evidenceDir)
	if err != nil {
		return fmt.Errorf("collect evidence from %s: %w", evidenceDir, err)
	}

	decisions := make([]curationDecision, 0, len(suite.Snapshots))
	for _, snap := range suite.Snapshots {
		decision := decideSnapshot(snap, statsBySnapshot[snap.ID])
		decisions = append(decisions, decision)
	}

	curated := make([]evolvemini.Snapshot, 0, len(decisions))
	dropped := make([]curationDecision, 0)
	for _, d := range decisions {
		if d.Drop {
			dropped = append(dropped, d)
			continue
		}
		curated = append(curated, d.Snapshot)
	}

	minKeep := len(suite.Snapshots) / 3
	if minKeep < 5 {
		minKeep = 5
	}
	if minKeep > len(suite.Snapshots) {
		minKeep = len(suite.Snapshots)
	}

	if len(curated) < minKeep && len(dropped) > 0 {
		restored := make(map[string]bool)
		sort.Slice(dropped, func(i, j int) bool {
			return dropped[i].RiskScore < dropped[j].RiskScore
		})
		need := minKeep - len(curated)
		if need > len(dropped) {
			need = len(dropped)
		}
		for i := 0; i < need; i++ {
			curated = append(curated, dropped[i].Snapshot)
			restored[dropped[i].Snapshot.ID] = true
		}
		for i := range decisions {
			if restored[decisions[i].Snapshot.ID] {
				decisions[i].Drop = false
				decisions[i].Reason = "restored_for_minimum_suite_size"
			}
		}
	}

	if len(curated) == 0 {
		return fmt.Errorf("curation removed all snapshots, aborting")
	}

	sort.Slice(curated, func(i, j int) bool {
		return curated[i].ID < curated[j].ID
	})

	result := &evolvemini.SnapshotSuite{Snapshots: curated}
	if err := result.Save(outputDir); err != nil {
		return fmt.Errorf("save curated suite: %w", err)
	}

	type reportDecision struct {
		ID        string `json:"id"`
		Drop      bool   `json:"drop"`
		Reason    string `json:"reason"`
		RiskScore int    `json:"riskScore"`
	}
	reportDecisions := make([]reportDecision, 0, len(decisions))
	for _, d := range decisions {
		reportDecisions = append(reportDecisions, reportDecision{
			ID:        d.Snapshot.ID,
			Drop:      d.Drop,
			Reason:    d.Reason,
			RiskScore: d.RiskScore,
		})
	}

	reportPath := filepath.Join(outputDir, "curation_report.txt")
	report := map[string]any{
		"source_suite":       suiteDir,
		"evidence_dir":       evidenceDir,
		"input_count":        len(suite.Snapshots),
		"output_count":       len(curated),
		"removed_count":      len(suite.Snapshots) - len(curated),
		"decisions":          reportDecisions,
		"generated_from_evo": len(statsBySnapshot),
	}
	reportData, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		_ = os.WriteFile(reportPath, reportData, 0o644)
		// Remove stale JSON report that would break LoadSuite.
		_ = os.Remove(filepath.Join(outputDir, "curation_report.json"))
	}

	fmt.Printf("Curated suite saved to %s\n", outputDir)
	fmt.Printf("Input: %d snapshots, Output: %d snapshots, Removed: %d\n",
		len(suite.Snapshots), len(curated), len(suite.Snapshots)-len(curated))
	fmt.Printf("Report: %s\n", reportPath)
	for _, d := range decisions {
		status := "KEEP"
		if d.Drop {
			status = "DROP"
		}
		fmt.Printf("  [%s] %s (%s)\n", status, d.Snapshot.ID, d.Reason)
	}
	return nil
}

func collectEvidenceStats(evidenceDir string) (map[string]snapshotEvidenceStats, error) {
	pattern := filepath.Join(evidenceDir, "gen_*", "cases", "*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]snapshotEvidenceStats)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var ev caseEvidenceRecord
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}

		snapshotID := strings.TrimSpace(ev.SnapshotID)
		if snapshotID == "" && ev.Score != nil {
			snapshotID = strings.TrimSpace(ev.Score.SnapshotID)
		}
		if snapshotID == "" {
			continue
		}

		st := stats[snapshotID]
		st.Observations++

		assessment := ""
		if ev.Score != nil {
			assessment = strings.ToLower(ev.Score.OverallAssessment)
		}

		workerFailed := ev.Worker == nil || !ev.Worker.Success
		judgeFailed := strings.Contains(assessment, "judge error")
		workerError := workerFailed || strings.Contains(assessment, "worker error")
		if workerError {
			st.WorkerErrors++
		}
		if judgeFailed {
			st.JudgeErrors++
		}
		if workerError || judgeFailed {
			st.Failures++
		}

		if ev.Worker != nil && ev.Worker.Success && ev.Worker.TokensAfter >= ev.Worker.TokensBefore {
			st.NonSaving++
		}

		stats[snapshotID] = st
	}
	return stats, nil
}

func decideSnapshot(snap evolvemini.Snapshot, st snapshotEvidenceStats) curationDecision {
	decision := curationDecision{
		Snapshot: snap,
		Drop:     false,
		Reason:   "stable_or_insufficient_evidence",
	}

	raw, _ := json.Marshal(snap)
	approxBytes := len(raw)

	reasons := make([]string, 0, 4)
	if approxBytes > 220_000 {
		reasons = append(reasons, fmt.Sprintf("oversized_snapshot_bytes=%d", approxBytes))
		decision.RiskScore += 5
	}
	if len(snap.RecentMessages) > 100 {
		reasons = append(reasons, fmt.Sprintf("too_many_messages=%d", len(snap.RecentMessages)))
		decision.RiskScore += 4
	}

	if st.Observations >= 2 {
		failureRate := float64(st.Failures) / float64(st.Observations)
		if failureRate >= 0.34 {
			reasons = append(reasons, fmt.Sprintf("high_failure_rate=%.2f", failureRate))
			decision.RiskScore += 6
		}
		if st.NonSaving == st.Observations {
			reasons = append(reasons, "always_non_saving")
			decision.RiskScore += 3
		}
	}
	if st.JudgeErrors >= 2 {
		reasons = append(reasons, fmt.Sprintf("repeated_judge_errors=%d", st.JudgeErrors))
		decision.RiskScore += 6
	}
	if st.WorkerErrors >= 2 {
		reasons = append(reasons, fmt.Sprintf("repeated_worker_errors=%d", st.WorkerErrors))
		decision.RiskScore += 6
	}

	if len(reasons) > 0 {
		decision.Drop = true
		decision.Reason = strings.Join(reasons, ";")
	}
	return decision
}
