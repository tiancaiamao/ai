package skill

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatsLoadNonexistentFile(t *testing.T) {
	s := LoadStats("/tmp/ai-skill-stats-nonexistent-" + t.Name() + ".json")
	if s.TopN != 10 {
		t.Errorf("expected TopN=10, got %d", s.TopN)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.Entries))
	}
	if s.Version != statsVersion {
		t.Errorf("expected Version=%d, got %d", statsVersion, s.Version)
	}
}

func TestStatsConcurrentRecordUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	s := LoadStats(path)

	// Simulate 15 concurrent find_skill calls all calling RecordUsage + Save
	const concurrency = 15
	done := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			name := []string{
				"skill", "agent", "web", "automate", "code",
				"debug", "plan", "tool", "notes", "docs",
				"search", "analyze", "security", "test", "deploy",
			}[idx]
			s.RecordUsage(name)
			done <- s.Save()
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		if err := <-done; err != nil {
			t.Errorf("goroutine %d: Save failed: %v", i, err)
		}
	}

	// Verify all 15 entries were recorded
	if len(s.Entries) != concurrency {
		t.Errorf("expected %d entries, got %d", concurrency, len(s.Entries))
	}

	// Verify file is valid JSON
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}

func TestStatsLoadEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	s := LoadStats(path)
	if s.TopN != 10 {
		t.Errorf("expected TopN=10, got %d", s.TopN)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.Entries))
	}
}

func TestStatsLoadMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("{not valid json}"), 0644); err != nil {
		t.Fatal(err)
	}
	s := LoadStats(path)
	if s.TopN != 10 {
		t.Errorf("expected TopN=10, got %d", s.TopN)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.Entries))
	}
}

func TestStatsRecordUsageCreatesEntry(t *testing.T) {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: filepath.Join(t.TempDir(), "stats.json"),
	}

	s.RecordUsage("my-skill")

	entry, ok := s.Entries["my-skill"]
	if !ok {
		t.Fatal("expected entry for my-skill")
	}
	if entry.Count != 1 {
		t.Errorf("expected Count=1, got %d", entry.Count)
	}
	if entry.Score != 1.0 {
		t.Errorf("expected Score=1.0, got %f", entry.Score)
	}
}

func TestStatsRecordUsageIncrementsCount(t *testing.T) {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: filepath.Join(t.TempDir(), "stats.json"),
	}

	s.RecordUsage("my-skill")
	s.RecordUsage("my-skill")
	s.RecordUsage("my-skill")

	entry := s.Entries["my-skill"]
	if entry.Count != 3 {
		t.Errorf("expected Count=3, got %d", entry.Count)
	}
	if entry.Score != 3.0 {
		t.Errorf("expected Score=3.0, got %f", entry.Score)
	}
}

func TestStatsTimeDecayOldEntryLowerScore(t *testing.T) {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: filepath.Join(t.TempDir(), "stats.json"),
	}

	now := time.Now()

	// Old skill: used 2 weeks ago (336 hours), Count=5
	s.Entries["old-skill"] = &SkillUsageEntry{
		Name:     "old-skill",
		Count:    5,
		LastUsed: now.Add(-336 * time.Hour),
		Score:    5.0,
	}

	// Recent skill: used just now, Count=5
	s.Entries["recent-skill"] = &SkillUsageEntry{
		Name:     "recent-skill",
		Count:    5,
		LastUsed: now,
		Score:    5.0,
	}

	top := s.TopSkills(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 results, got %d", len(top))
	}
	if top[0] != "recent-skill" {
		t.Errorf("expected recent-skill first, got %s", top[0])
	}
	if top[1] != "old-skill" {
		t.Errorf("expected old-skill second, got %s", top[1])
	}

	// Verify the decay math: old-skill should have Score * 0.5^(336/168) = 5 * 0.25 = 1.25
	oldEffective := 5.0 * math.Pow(0.5, 336.0/168.0)
	recentEffective := 5.0 * math.Pow(0.5, 0)
	if math.Abs(oldEffective-1.25) > 0.01 {
		t.Errorf("expected old effective ~1.25, got %f", oldEffective)
	}
	if math.Abs(recentEffective-5.0) > 0.01 {
		t.Errorf("expected recent effective ~5.0, got %f", recentEffective)
	}
}

func TestStatsTopSkillsOrderingAndLimit(t *testing.T) {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: filepath.Join(t.TempDir(), "stats.json"),
	}

	now := time.Now()

	// Three skills with different scores
	s.Entries["low"] = &SkillUsageEntry{
		Name: "low", Count: 1, LastUsed: now, Score: 1.0,
	}
	s.Entries["high"] = &SkillUsageEntry{
		Name: "high", Count: 10, LastUsed: now, Score: 10.0,
	}
	s.Entries["mid"] = &SkillUsageEntry{
		Name: "mid", Count: 5, LastUsed: now, Score: 5.0,
	}

	// Top 2
	top2 := s.TopSkills(2)
	if len(top2) != 2 {
		t.Fatalf("expected 2, got %d", len(top2))
	}
	if top2[0] != "high" || top2[1] != "mid" {
		t.Errorf("expected [high, mid], got %v", top2)
	}

	// Top 0
	top0 := s.TopSkills(0)
	if len(top0) != 0 {
		t.Errorf("expected empty for TopSkills(0), got %v", top0)
	}

	// Top negative
	topNeg := s.TopSkills(-1)
	if len(topNeg) != 0 {
		t.Errorf("expected empty for TopSkills(-1), got %v", topNeg)
	}

	// Top larger than entries
	topAll := s.TopSkills(10)
	if len(topAll) != 3 {
		t.Errorf("expected 3, got %d", len(topAll))
	}
	if topAll[0] != "high" || topAll[1] != "mid" || topAll[2] != "low" {
		t.Errorf("expected [high, mid, low], got %v", topAll)
	}
}

func TestStatsSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: path,
	}

	s.RecordUsage("skill-a")
	s.RecordUsage("skill-b")
	s.RecordUsage("skill-a")

	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := LoadStats(path)
	if loaded.TopN != s.TopN {
		t.Errorf("TopN mismatch: %d vs %d", loaded.TopN, s.TopN)
	}

	if len(loaded.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Entries))
	}

	entryA := loaded.Entries["skill-a"]
	if entryA == nil {
		t.Fatal("skill-a not found")
	}
	if entryA.Count != 2 {
		t.Errorf("skill-a Count: expected 2, got %d", entryA.Count)
	}

	entryB := loaded.Entries["skill-b"]
	if entryB == nil {
		t.Fatal("skill-b not found")
	}
	if entryB.Count != 1 {
		t.Errorf("skill-b Count: expected 1, got %d", entryB.Count)
	}

	// Verify JSON is valid
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}
