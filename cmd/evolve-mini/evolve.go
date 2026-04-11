package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

const (
	MaxGenerations = 10
	MaxNoImprove   = 3
)

func runEvolve(suiteDir string, maxGen int) error {
	if maxGen <= 0 {
		maxGen = MaxGenerations
	}

	fmt.Printf("=== evolve-mini: Starting evolution ===\n")
	fmt.Printf("Suite: %s\n", suiteDir)
	fmt.Printf("Max generations: %d\n\n", maxGen)

	baseRepo, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	worktreeMgr, err := InitializeWorktreeManager(baseRepo)
	if err != nil {
		return fmt.Errorf("init worktree manager: %w", err)
	}

	// Load or resume history
	historyPath := filepath.Join(baseRepo, "data", "generations", "history.json")
	history := loadOrNewHistory(historyPath)

	// --- Generation 0: Baseline ---
	if history.BestScore == nil {
		baselineWorkerPath := filepath.Join(baseRepo, "evolve-mini-worker")
		if _, err := os.Stat(baselineWorkerPath); err != nil {
			return fmt.Errorf("baseline worker not found (run 'go build ./cmd/evolve-mini-worker/' first): %w", err)
		}

		fmt.Println("=== Generation 0 (baseline) ===")
		baselineScore, err := runScore(baselineWorkerPath, suiteDir, 0)
		if err != nil {
			return fmt.Errorf("baseline scoring failed: %w", err)
		}

		history.BestScore = &baselineScore
		history.BestGeneration = 0
		history.AllGenerations = append(history.AllGenerations, evolvemini.GenerationRecord{
			Generation:        0,
			ParentGen:         -1,
			Status:            "accepted",
			OptimizeRationale: "baseline",
			SuiteScore:        &baselineScore,
			CreatedAt:         time.Now().Format(time.RFC3339),
		})
		if err := saveHistory(history, baseRepo); err != nil {
			return fmt.Errorf("save baseline history: %w", err)
		}
		fmt.Printf("\nBaseline scoring complete (WA=%.1f)\n", baselineScore.WeightedAverage)
	} else {
		fmt.Printf("=== Baseline already scored (WA=%.1f), skipping ===\n\n", history.BestScore.WeightedAverage)
	}

	// Clean checkpoint so scoring each gen starts fresh
	checkpointPath := filepath.Join(suiteDir, "checkpoint.json")
	_ = os.Remove(checkpointPath)

	// --- Evolution loop ---
	agent := NewOptimizeAgent(baseRepo, suiteDir)
	noImproveCount := 0

	startGen := history.CurrentGeneration + 1
	for gen := startGen; gen <= maxGen; gen++ {
		fmt.Printf("\n=== Generation %d ===\n", gen)
		history.CurrentGeneration = gen

		// Step 1: Create worktree + run optimizer
		fmt.Println("Step 1: Creating worktree and running optimizer...")
		ws, err := worktreeMgr.CreateWorktree(gen)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		fmt.Printf("Worktree: %s\n", ws.Worktree)

		err = agent.RunOptimizer(context.Background(), ws, history)
		if err != nil {
			fmt.Printf("Optimizer error: %v\n", err)
			_ = worktreeMgr.Cleanup(gen)
			history.AllGenerations = append(history.AllGenerations, evolvemini.GenerationRecord{
				Generation: gen,
				ParentGen:  gen - 1,
				Status:     "failed",
				Error:      fmt.Sprintf("optimizer: %v", err),
				CreatedAt:  time.Now().Format(time.RFC3339),
			})
			_ = saveHistory(history, baseRepo)
			noImproveCount++
			if noImproveCount >= MaxNoImprove {
				fmt.Printf("Stopping: %d consecutive failures\n", MaxNoImprove)
				break
			}
			continue
		}

		// Step 2: Build worker in worktree
		fmt.Println("\nStep 2: Building worker binary...")
		workerPath, err := worktreeMgr.BuildWorker(ws)
		if err != nil {
			fmt.Printf("Build error: %v\n", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}
		fmt.Printf("Worker: %s\n", workerPath)

		// Step 3: Score worker
		fmt.Println("\nStep 3: Scoring...")
		// Clean checkpoint for fresh scoring
		_ = os.Remove(checkpointPath)
		genScore, err := runScore(workerPath, suiteDir, gen)
		if err != nil {
			fmt.Printf("Score error: %v\n", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}

		// Step 4: Compare and accept/reject
		delta := genScore.WeightedAverage - history.BestScore.WeightedAverage
		genScore.DeltaVsBaseline = delta
		fmt.Printf("\nGen %d WA=%.1f vs Baseline WA=%.1f (delta=%.1f)\n",
			gen, genScore.WeightedAverage, history.BestScore.WeightedAverage, delta)

		accepted := delta > 0
		record := evolvemini.GenerationRecord{
			Generation:        gen,
			ParentGen:         gen - 1,
			SuiteScore:        &genScore,
			CreatedAt:         time.Now().Format(time.RFC3339),
		}

		if accepted {
			record.Status = "accepted"
			record.OptimizeRationale = fmt.Sprintf("WA improved by %.1f", delta)
			history.BestGeneration = gen
			history.BestScore = &genScore

			// Commit accepted changes to evolve-baseline branch
			if err := worktreeMgr.CommitBaseline(ws); err != nil {
				fmt.Printf("Warning: failed to commit baseline: %v\n", err)
			}
			fmt.Printf("✓ Accepted! New best: %.1f\n", genScore.WeightedAverage)
			noImproveCount = 0
		} else {
			record.Status = "rejected"
			record.OptimizeRationale = fmt.Sprintf("WA did not improve (delta=%.1f)", delta)
			_ = worktreeMgr.Cleanup(gen)
			fmt.Printf("✗ Rejected (delta=%.1f)\n", delta)
			noImproveCount++
		}

		history.AllGenerations = append(history.AllGenerations, record)
		if err := saveHistory(history, baseRepo); err != nil {
			fmt.Printf("Warning: failed to save history: %v\n", err)
		}

		if noImproveCount >= MaxNoImprove {
			fmt.Printf("\nStopping: no improvement for %d generations\n", MaxNoImprove)
			break
		}
	}

	// Final summary
	fmt.Printf("\n=== Evolution complete ===\n")
	fmt.Printf("Best generation: %d (WA=%.1f)\n", history.BestGeneration, history.BestScore.WeightedAverage)
	fmt.Printf("Total generations: %d\n", len(history.AllGenerations))
	return nil
}

// loadOrNewHistory loads existing history or creates a new one.
func loadOrNewHistory(path string) *EvolutionHistory {
	data, err := os.ReadFile(path)
	if err != nil {
		return &EvolutionHistory{
			CurrentGeneration: 0,
			BestGeneration:    0,
			BestScore:        nil,
			AllGenerations:  []evolvemini.GenerationRecord{},
		}
	}
	var h EvolutionHistory
	if err := json.Unmarshal(data, &h); err != nil {
		fmt.Printf("Warning: corrupt history.json, starting fresh\n")
		return &EvolutionHistory{
			CurrentGeneration: 0,
			BestGeneration:    0,
			BestScore:        nil,
			AllGenerations:  []evolvemini.GenerationRecord{},
		}
	}
	return &h
}

// saveHistory persists evolution history to disk.
func saveHistory(history *EvolutionHistory, baseRepo string) error {
	historyPath := filepath.Join(baseRepo, "data", "generations", "history.json")
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(historyPath, data, 0o644)
}