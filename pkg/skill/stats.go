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
	Name      string    `json:"name"`
	Count     int       `json:"count"`
	LastUsed  time.Time `json:"lastUsed"`
	LastShown time.Time `json:"lastShown"`
	Score     float64   `json:"score"`
}

// SkillStatsFile holds persisted skill usage statistics.
type SkillStatsFile struct {
	mu       sync.Mutex                  `json:"-"`
	Version  int                         `json:"version"`
	TopN     int                         `json:"topN"`
	Entries  map[string]*SkillUsageEntry `json:"entries"`
	FilePath string                      `json:"-"`
}

const (
	statsVersion          = 1
	DefaultTopN           = 10
	decayHalfLifeSessions = 4.0 // score halves every 4 agent sessions
)

// sessionDecayFactor is the multiplier applied to every score once per
// agent session start.
var sessionDecayFactor = math.Pow(0.5, 1/decayHalfLifeSessions)

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

// DecayForNewSession applies one decay step to all skill scores. Call it
// exactly once per agent session start, before the first usage record.
// Time is measured in agent sessions, not wall-clock time: while the agent
// is idle, scores do not decay.
func (s *SkillStatsFile) DecayForNewSession() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.Entries {
		entry.Score *= sessionDecayFactor
	}
}

// RecordUsage records one use of skillName: Count is incremented and Score
// gains +1 on top of the session-decayed value.
func (s *SkillStatsFile) RecordUsage(skillName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.Entries[skillName]
	if !ok {
		entry = &SkillUsageEntry{Name: skillName}
		s.Entries[skillName] = entry
	}

	entry.Count++
	entry.LastUsed = time.Now()
	entry.Score++
}

// RecordShown marks the given skills as shown in the prompt (LastShown).
// Persistence happens via Save, which the prompt builder performs after
// formatting.
func (s *SkillStatsFile) RecordShown(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, name := range names {
		entry, ok := s.Entries[name]
		if !ok {
			entry = &SkillUsageEntry{Name: name}
			s.Entries[name] = entry
		}
		entry.LastShown = now
	}
}

// LastShownOf returns the last-shown timestamp for a skill (zero time if the
// skill has never been shown).
func (s *SkillStatsFile) LastShownOf(name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.Entries[name]; ok {
		return entry.LastShown
	}
	return time.Time{}
}

// mergeWithDisk folds the on-disk entries into s so a Save from a concurrent
// process doesn't overwrite updates that happened since s loaded the file.
// The merge is monotonic: per-entry max of Score/Count/LastUsed/LastShown,
// union of entries. Consequence: a skill's score never decreases on disk, so
// concurrent sessions decay slightly slower than one step per session —
// harmless for ranking. A missing or unreadable file is ignored (fresh start).
func (s *SkillStatsFile) mergeWithDisk() {
	data, err := os.ReadFile(s.FilePath)
	if err != nil || len(data) == 0 {
		return
	}
	var existing SkillStatsFile
	if err := json.Unmarshal(data, &existing); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, e := range existing.Entries {
		ours, ok := s.Entries[name]
		if !ok {
			s.Entries[name] = e
			continue
		}
		if e.Score > ours.Score {
			ours.Score = e.Score
		}
		if e.Count > ours.Count {
			ours.Count = e.Count
		}
		if e.LastUsed.After(ours.LastUsed) {
			ours.LastUsed = e.LastUsed
		}
		if e.LastShown.After(ours.LastShown) {
			ours.LastShown = e.LastShown
		}
	}
}

// Save writes the stats to s.FilePath atomically using write-to-temp + rename.
// Before writing, it merges the on-disk copy (mergeWithDisk) so concurrent
// agent processes sharing the same stats file don't clobber each other's
// updates with a stale in-memory snapshot. The caller must NOT hold s.mu;
// Save acquires it internally.
func (s *SkillStatsFile) Save() error {
	if s.FilePath == "" {
		return nil // nothing to persist (e.g. stats built in tests)
	}
	s.mergeWithDisk()

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

	if err := os.Rename(tmpPath, s.FilePath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// SortByScore returns the given names sorted by descending score.
// Names without a stats entry sort last, in input order. Used to rank
// omitted skill names for the find_skill keyword hint.
func (s *SkillStatsFile) SortByScore(names []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	scores := make([]float64, len(names))
	for i, name := range names {
		if entry, ok := s.Entries[name]; ok {
			scores[i] = entry.Score
		}
	}

	idx := make([]int, len(names))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return scores[idx[a]] > scores[idx[b]]
	})

	result := make([]string, len(names))
	for i, j := range idx {
		result[i] = names[j]
	}
	return result
}

// TopSkills returns up to n skill names sorted by descending score. Scores
// are session-decayed: DecayForNewSession is applied once per agent session
// start, so idle time does not decay scores. If n <= 0, it returns an empty
// slice.
func (s *SkillStatsFile) TopSkills(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n <= 0 {
		return nil
	}

	type scored struct {
		name  string
		score float64
	}

	candidates := make([]scored, 0, len(s.Entries))
	for name, entry := range s.Entries {
		candidates = append(candidates, scored{name: name, score: entry.Score})
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
