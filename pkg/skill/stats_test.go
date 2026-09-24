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

func TestStatsSessionDecay(t *testing.T) {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: filepath.Join(t.TempDir(), "stats.json"),
	}

	now := time.Now()

	// Scores are compared as stored: LastUsed no longer affects ranking.
	// "old-skill" has the same score but was last used long ago — they tie
	// and name order breaks the tie (old-skill before recent-skill).
	s.Entries["old-skill"] = &SkillUsageEntry{
		Name:     "old-skill",
		Count:    5,
		LastUsed: now.Add(-336 * time.Hour),
		Score:    5.0,
	}
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
	if top[0] != "old-skill" || top[1] != "recent-skill" {
		t.Errorf("expected name-order tie-break [old-skill recent-skill], got %v", top)
	}

	// Four session-start decay steps halve every score.
	for i := 0; i < 4; i++ {
		s.DecayForNewSession()
	}
	if math.Abs(s.Entries["old-skill"].Score-2.5) > 1e-9 {
		t.Errorf("expected old-skill score ~2.5 after 4 sessions, got %f", s.Entries["old-skill"].Score)
	}
	if math.Abs(s.Entries["recent-skill"].Score-2.5) > 1e-9 {
		t.Errorf("expected recent-skill score ~2.5 after 4 sessions, got %f", s.Entries["recent-skill"].Score)
	}

	// One use adds +1 on top of the decayed score.
	s.RecordUsage("recent-skill")
	if math.Abs(s.Entries["recent-skill"].Score-3.5) > 1e-9 {
		t.Errorf("expected recent-skill score ~3.5 after one use, got %f", s.Entries["recent-skill"].Score)
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

func TestStatsSortByScore(t *testing.T) {
	s := &SkillStatsFile{
		Version: 1,
		TopN:    10,
		Entries: map[string]*SkillUsageEntry{
			"fresh": {Name: "fresh", Count: 10, LastUsed: time.Now(), Score: 10},
			"stale": {Name: "stale", Count: 12, LastUsed: time.Now().Add(-24 * time.Hour), Score: 5},
		},
	}

	// Scores are compared as stored (session-decayed); "stale" has a lower
	// decayed score, "fresh" a higher one.
	got := s.SortByScore([]string{"stale", "missing", "fresh"})
	if len(got) != 3 || got[0] != "fresh" || got[1] != "stale" || got[2] != "missing" {
		t.Errorf("expected [fresh stale missing], got %v", got)
	}

	// Input without any stats entries: stable input order preserved.
	got = s.SortByScore([]string{"a", "b", "c"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("expected stable order [a b c], got %v", got)
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

// TestStatsSaveMergesConcurrentEntries verifies that Save merges with the
// on-disk copy (per-entry max, union of entries) instead of last-write-wins,
// so concurrent agent processes don't clobber each other's updates.
func TestStatsSaveMergesConcurrentEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	// Another process's state on disk: newer "a" values, plus "c" we don't know about.
	diskState := &SkillStatsFile{
		Version: statsVersion,
		TopN:    DefaultTopN,
		Entries: map[string]*SkillUsageEntry{
			"a": {Name: "a", Count: 5, Score: 10, LastUsed: time.Now().Add(time.Hour), LastShown: time.Now().Add(2 * time.Hour)},
			"c": {Name: "c", Count: 1, Score: 1},
		},
		FilePath: path,
	}
	if err := diskState.Save(); err != nil {
		t.Fatalf("seed Save failed: %v", err)
	}

	// Our process loaded the file earlier (stale "a"), recorded "b" in the meantime.
	ours := &SkillStatsFile{
		Version: statsVersion,
		TopN:    DefaultTopN,
		Entries: map[string]*SkillUsageEntry{
			"a": {Name: "a", Count: 3, Score: 8, LastUsed: time.Now().Add(-time.Hour)},
			"b": {Name: "b", Count: 2, Score: 4, LastUsed: time.Now()},
		},
		FilePath: path,
	}
	if err := ours.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	merged := LoadStats(path)
	if len(merged.Entries) != 3 {
		t.Fatalf("expected 3 entries after merge, got %d", len(merged.Entries))
	}

	a := merged.Entries["a"]
	if a == nil {
		t.Fatal("a not found")
	}
	if a.Score != 10 {
		t.Errorf("a.Score: expected max 10, got %v", a.Score)
	}
	if a.Count != 5 {
		t.Errorf("a.Count: expected max 5, got %d", a.Count)
	}
	if a.LastUsed.IsZero() {
		t.Errorf("a.LastUsed: expected newer timestamp from disk, got zero")
	}
	if a.LastShown.IsZero() {
		t.Errorf("a.LastShown: expected newer timestamp from disk, got zero")
	}

	if b := merged.Entries["b"]; b == nil || b.Score != 4 {
		t.Errorf("b: expected our entry to survive merge, got %+v", b)
	}
	if c := merged.Entries["c"]; c == nil || c.Score != 1 {
		t.Errorf("c: expected disk-only entry to be merged in, got %+v", c)
	}
}
