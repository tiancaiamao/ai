package main

import "github.com/tiancaiamao/ai/internal/evolvemini"

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// runStatus shows the evolution status.
func runStatus(evolveDir string) error {
	scoreFile := filepath.Join(evolveDir, "score.json")
	data, err := os.ReadFile(scoreFile)
	if err != nil {
		return fmt.Errorf("no score.json found in %s: %w", evolveDir, err)
	}
	var ss evolvemini.SuiteScore
	if err := json.Unmarshal(data, &ss); err != nil {
		return fmt.Errorf("parse score.json: %w", err)
	}
	printScoreTable(ss)
	return nil
}
