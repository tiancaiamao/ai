package skill

import (
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SkillUsageEntry tracks usage statistics for a single skill.
type SkillUsageEntry struct {
	Name     string    `json:"name"`
	Count    int       `json:"count"`
	LastUsed time.Time `json:"lastUsed"`
	Score    float64   `json:"score"`
}

// SkillStatsFile holds persisted skill usage statistics.
type SkillStatsFile struct {
	mu       sync.Mutex                   `json:"-"`
	Version  int                         `json:"version"`
	TopN     int                         `json:"topN"`
	Entries  map[string]*SkillUsageEntry `json:"entries"`
	FilePath string                      `json:"-"`
}

const (
	statsVersion       = 1
	DefaultTopN        = 10
	decayHalfLifeHours = 168.0 // 1 week
)

// LoadStats reads skill usage statistics from a JSON file at path.
// If the file is missing or contains invalid JSON, it returns an empty
// SkillStatsFile with TopN=10 and no error.
func LoadStats(path string) *SkillStatsFile {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     DefaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			// File exists but unreadable — log but don't overwrite on Save.
			slog.Warn("[SkillStats] failed to read stats file, starting fresh",
				"path", path, "error", err)
		}
		return s
	}

	if len(data) == 0 {
		slog.Warn("[SkillStats] stats file is empty, starting fresh",
			"path", path)
		return s
	}

	var file struct {
		Version int                         `json:"version"`
		TopN    int                         `json:"topN"`
		Entries map[string]*SkillUsageEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		slog.Warn("[SkillStats] stats file has invalid JSON, starting fresh",
			"path", path, "error", err)
		return s
	}

		s.Version = file.Version
	if file.TopN > 0 {
		s.TopN = file.TopN
	}
	// Upgrade stale topN values to current default
	if s.TopN < DefaultTopN {
		s.TopN = DefaultTopN
	}
	if file.Entries != nil {
		s.Entries = file.Entries
	}

	return s
}

// RecordUsage increments the usage count for skillName, updates LastUsed,
// and recomputes Score using half-life decay:
//
//	Score = Count * 0.5^(hours_since_last_use / 168)
//
// For a new entry, hours_since_last_use is 0 so Score = Count.
// For an existing entry, the previous LastUsed determines decay before
// the increment. After incrementing Count and setting LastUsed to now,
// Score is recomputed as the new Count (since hours since new LastUsed = 0).
func (s *SkillStatsFile) RecordUsage(skillName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	entry, ok := s.Entries[skillName]
	if !ok {
		entry = &SkillUsageEntry{Name: skillName}
		s.Entries[skillName] = entry
	}

	entry.Count++
	entry.LastUsed = now
	entry.Score = float64(entry.Count)
}

// Save writes the stats to s.FilePath atomically using write-to-temp + rename.
// The caller must NOT hold s.mu; Save acquires it internally.
func (s *SkillStatsFile) Save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}

	// Atomic write: temp file in same directory, then rename.
	// This prevents concurrent Save() calls from corrupting the file
	// (e.g. O_TRUNC race leaving a 0-byte file).
	dir := filepath.Dir(s.FilePath)
	tmp, err := os.CreateTemp(dir, ".skill-stats-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, s.FilePath)
}

// TopSkills returns up to n skill names sorted by descending time-decayed score.
// The effective score for ranking applies decay based on time elapsed since
// each skill's LastUsed:
//
//	effectiveScore = Score * 0.5^(hours_since_last_use / 168)
//
// which equals Count * 0.5^(hours_since_last_use / 168).
// If n <= 0, it returns an empty slice.
func (s *SkillStatsFile) TopSkills(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n <= 0 {
		return nil
	}

	now := time.Now()

	type scored struct {
		name  string
		score float64
	}

	candidates := make([]scored, 0, len(s.Entries))
	for name, entry := range s.Entries {
		hoursSince := now.Sub(entry.LastUsed).Hours()
		effectiveScore := entry.Score * math.Pow(0.5, hoursSince/decayHalfLifeHours)
		candidates = append(candidates, scored{name: name, score: effectiveScore})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})

	if n > len(candidates) {
		n = len(candidates)
	}

	result := make([]string, n)
	for i := range n {
		result[i] = candidates[i].name
	}
	return result
}