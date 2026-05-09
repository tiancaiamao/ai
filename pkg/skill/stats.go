package skill

import (
	"encoding/json"
	"math"
	"os"
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
		defaultTopN        = 10
	decayHalfLifeHours = 168.0 // 1 week
)

// LoadStats reads skill usage statistics from a JSON file at path.
// If the file is missing or contains invalid JSON, it returns an empty
// SkillStatsFile with TopN=10 and no error.
func LoadStats(path string) *SkillStatsFile {
	s := &SkillStatsFile{
		Version:  statsVersion,
		TopN:     defaultTopN,
		Entries:  make(map[string]*SkillUsageEntry),
		FilePath: path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}

	if len(data) == 0 {
		return s
	}

	var file struct {
		Version int                         `json:"version"`
		TopN    int                         `json:"topN"`
		Entries map[string]*SkillUsageEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return s
	}

	s.Version = file.Version
	if file.TopN > 0 {
		s.TopN = file.TopN
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

// Save writes the stats to s.FilePath as JSON.
// Caller must NOT hold s.mu; Save acquires it internally.
func (s *SkillStatsFile) Save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}

	f, err := os.OpenFile(s.FilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
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