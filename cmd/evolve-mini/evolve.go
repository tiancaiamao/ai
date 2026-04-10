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
	MaxNoImprove   = 5
)

type EvolveHistory struct {
	CurrentGeneration int
	BestScore        *evolvemini.SuiteScore
	AllGenerations  []evolvemini.GenerationRecord
}

func runEvolve(suiteDir string, maxGen int) error {
	if maxGen <= 0 {
		maxGen = MaxGenerations
	}

	fmt.Printf("=== evolve-mini: Starting evolution ===
")
	fmt.Printf("Suite: %s
", suiteDir)
	fmt.Printf("Max generations: %d
", maxGen)
	fmt.Printf()

	// Initialize workspace
	baseRepo, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	worktreeMgr, err := InitializeWorktreeManager(baseRepo)
	if err != nil {
		return fmt.Errorf("init worktree manager: %w", err)
	}

	// Load suite
	suite, err := evolvemini.LoadSuite(suiteDir)
	if err != nil {
		return fmt.Errorf("load suite: %w", err)
	}
	fmt.Printf("Loaded %d snapshots from suite

", len(suite.Snapshots))

	// Initialize history
	history := &EvolveHistory{
		CurrentGeneration: 0,
		BestScore:        nil,
		AllGenerations:  []evolvemini.GenerationRecord{},
	}

	// Generation 0 = baseline (current code in main branch)
	baselineWorkerPath := filepath.Join(baseRepo, "evolve-mini-worker")
	if _, err := os.Stat(baselineWorkerPath); err != nil {
		return fmt.Errorf("baseline worker not found (run 'go build ./cmd/evolve-mini-worker/' first): %w", err)
	}

	fmt.Println("=== Generation 0 (baseline) ===")
	baselineScore, err := scoreWorker(baselineWorkerPath, suite, 0)
	if err != nil {
		return fmt.Errorf("baseline scoring failed: %w", err)
	}
	printScoreTable(*baselineScore)

	history.BestScore = baselineScore
	recordGen := evolvemini.GenerationRecord{
		Generation:   0,
		ParentGen:    -1,
		Status:       "scored",
		SuiteScore:   baselineScore,
		CreatedAt:    time.Now().Format(time.RFC3339),
		UpdatedAt:    time.Now().Format(time.RFC3339),
	}
	history.AllGenerations = append(history.AllGenerations, recordGen)

	fmt.Printf("
Baseline score: %.1f

", baselineScore.WeightedAverage)

	// Evolution loop
	agent := NewOptimizeAgent(llm.Model{ID: "gpt-4o"}, os.Getenv("ZAI_API_KEY"))
	noImproveCount := 0

	for gen := 1; gen <= maxGen; gen++ {
		fmt.Printf("
=== Generation %d ===
", gen)
		history.CurrentGeneration = gen

		// Step 1: Generate mutation
		fmt.Println("Step 1: Generating mutation...")
		mutation, err := agent.GenerateMutation(context.Background(), history)
		if err != nil {
			fmt.Printf("Error: %v
", err)
			noImproveCount++
			if noImproveCount >= MaxNoImprove {
				fmt.Printf("Stopping: no improvement for %d generations
", MaxNoImprove)
				break
			}
			continue
		}
		fmt.Printf("Analysis: %s
", mutation.Analysis)
		fmt.Printf("Files to modify: %d
", len(mutation.FileChanges))

		// Step 2: Create worktree and apply patch
		fmt.Println("
Step 2: Creating worktree...")
		ws, err := worktreeMgr.CreateWorktree(gen, history.CurrentGeneration-1)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		fmt.Printf("Worktree: %s
", ws.Worktree)

		fmt.Println("
Step 3: Applying code changes...")
		for filepath, content := range mutation.FileChanges {
			fullPath := filepath.Join(ws.Src, filepath)
			if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write file %s: %w", filepath, err)
			}
			fmt.Printf("  Modified: %s
", filepath)
		}

		// Step 4: Build worker
		fmt.Println("
Step 4: Building worker binary...")
		workerPath, err := worktreeMgr.BuildWorker(ws)
		if err != nil {
			fmt.Printf("Error: %v
", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}
		fmt.Printf("Worker: %s
", workerPath)

		// Step 5: Score worker
		fmt.Println("
Step 5: Scoring...")
		suiteScore, err := scoreWorker(workerPath, suite, gen)
		if err != nil {
			fmt.Printf("Error: %v
", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}
		printScoreTable(*suiteScore)

		// Step 6: Compare
		fmt.Printf("
Step 6: Comparing to best (%.1f)...
", history.BestScore.WeightedAverage)
		delta := suiteScore.WeightedAverage - history.BestScore.WeightedAverage
		improved := delta > 0
		minScoreOK := suiteScore.MinScore >= history.BestScore.MinScore*0.9 // Allow 10% regression
		regressed := suiteScore.RegressedCases > 2

		fmt.Printf("Delta: %.2f
", delta)
		fmt.Printf("Improved: %v (minScoreOK: %v, regressedCases: %d)
",
			improved, minScoreOK, suiteScore.RegressedCases)

		// Record generation
		recordGen := evolvemini.GenerationRecord{
			Generation:   gen,
			ParentGen:    history.CurrentGeneration - 1,
			Status:       "scored",
			OptimizeRationale: mutation.Analysis,
			SuiteScore:   suiteScore,
			CreatedAt:    time.Now().Format(time.RFC3339),
			UpdatedAt:    time.Now().Format(time.RFC3339),
		}
		history.AllGenerations = append(history.AllGenerations, recordGen)

		// Accept if better
		if improved && minScoreOK && !regressed {
			fmt.Printf("
✓ ACCEPTED as new best!
")
			history.BestScore = suiteScore
			noImproveCount = 0
		} else {
			fmt.Printf("
✗ REJECTED
")
			fmt.Printf("  Reason: ")
			if !improved {
				fmt.Println("score not better")
			} else if !minScoreOK {
				fmt.Printf("worst case dropped from %.0f to %.0f
",
					history.BestScore.MinScore, suiteScore.MinScore)
			} else if regressed {
				fmt.Printf("too many regressed cases (%d > 2)
", suiteScore.RegressedCases)
			}

			// Clean up failed generation
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
		}

		// Check stop condition
		if noImproveCount >= MaxNoImprove {
			fmt.Printf("
Stopping: no improvement for %d generations
", MaxNoImprove)
			break
		}
	}

	// Final summary
	fmt.Printf("
=== Evolution complete ===
")
	fmt.Printf("Total generations: %d
", len(history.AllGenerations))
	if history.BestScore != nil {
		fmt.Printf("Best score: %.1f (gen %d)
",
			history.BestScore.WeightedAverage,
			history.BestScore.Generation)
	}

	// Save history
	historyPath := filepath.Join(baseRepo, "data", "generations", "history.json")
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
		data, _ := json.MarshalIndent(history, "", "  ")
		_ = os.WriteFile(historyPath, data, 0o644)
		fmt.Printf("
History saved to: %s
", historyPath)
	}

	return nil
}