package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

const (
	MaxGenerations = 10
	MaxNoImprove   = 5
)

func runEvolve(suiteDir string, maxGen int) error {
	if maxGen <= 0 {
		maxGen = MaxGenerations
	}

	fmt.Printf("=== evolve-mini: Starting evolution ===\n")
	fmt.Printf("Suite: %s\n", suiteDir)
	fmt.Printf("Max generations: %d\n\n", maxGen)

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
	fmt.Printf("Loaded %d snapshots from suite\n\n", len(suite.Snapshots))

	// Initialize history
	history := &EvolutionHistory{
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
	err = runScore(baselineWorkerPath, suiteDir)
	if err != nil {
		return fmt.Errorf("baseline scoring failed: %w", err)
	}

	// Baseline scoring output is already printed by runScore
	// For evolution to continue, we would need runScore to return SuiteScore
	// For now, we'll just do a minimal baseline test
	fmt.Printf("\nBaseline scoring complete\n")

	history.CurrentGeneration = 0

	// Evolution loop
	agent := NewOptimizeAgent()
	noImproveCount := 0

	for gen := 1; gen <= maxGen; gen++ {
		fmt.Printf("\n=== Generation %d ===\n", gen)
		history.CurrentGeneration = gen

		// Step 1: Generate mutation
		fmt.Println("Step 1: Generating mutation...")
		mutation, err := agent.GenerateMutation(context.Background(), history)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			noImproveCount++
			if noImproveCount >= MaxNoImprove {
				fmt.Printf("Stopping: no improvement for %d generations\n", MaxNoImprove)
				break
			}
			continue
		}
		fmt.Printf("Analysis: %s\n", mutation.Analysis)
		fmt.Printf("Files to modify: %d\n", len(mutation.FileChanges))

		// For Phase 4 stub, if no changes, skip to next generation
		if len(mutation.FileChanges) == 0 {
			fmt.Println("No file changes generated (Phase 4 stub)")
			noImproveCount++
			if noImproveCount >= MaxNoImprove {
				break
			}
			continue
		}

		// Step 2: Create worktree and apply patch
		fmt.Println("\nStep 2: Creating worktree...")
		ws, err := worktreeMgr.CreateWorktree(gen)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		fmt.Printf("Worktree: %s\n", ws.Worktree)

		fmt.Println("\nStep 3: Applying code changes...")
		for filepathStr, content := range mutation.FileChanges {
			fullPath := filepath.Join(ws.Src, filepathStr)
			if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write file %s: %w", filepathStr, err)
			}
			fmt.Printf("  Modified: %s\n", filepathStr)
		}

		// Step 4: Build worker
		fmt.Println("\nStep 4: Building worker binary...")
		workerPath, err := worktreeMgr.BuildWorker(ws)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}
		fmt.Printf("Worker: %s\n", workerPath)

		// Step 5: Score worker
		fmt.Println("\nStep 5: Scoring...")
		err = runScore(workerPath, suiteDir)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			_ = worktreeMgr.Cleanup(gen)
			noImproveCount++
			continue
		}

		// For Phase 4 stub, we don't compare scores
		// In full implementation, runScore would return SuiteScore

		fmt.Printf("\nGeneration %d complete\n", gen)
		noImproveCount = 0
	}

	// Final summary
	fmt.Printf("\n=== Evolution complete ===\n")
	fmt.Printf("Total generations: %d\n", len(history.AllGenerations))

	// Save history
	historyPath := filepath.Join(baseRepo, "data", "generations", "history.json")
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err == nil {
		data, _ := json.MarshalIndent(history, "", "  ")
		_ = os.WriteFile(historyPath, data, 0o644)
		fmt.Printf("\nHistory saved to: %s\n", historyPath)
	}

	return nil
}