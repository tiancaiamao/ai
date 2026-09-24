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
//
// Score is the entry's effective value as of the DecayStep recorded in
// LastDecay; the effective value at the current DecayStep is
// Score * sessionDecayFactor^(DecayStep - LastDecay). RecordUsage and
// RecordShown refresh the entry, re-anchoring LastDecay at the current
// DecayStep.
type SkillUsageEntry struct {
	Name      string    `json:"name"`
	Count     int       `json:"count"`
	LastUsed  time.Time `json:"lastUsed"`
	LastShown time.Time `json:"lastShown"`
	Score     float64   `json:"score"`
	LastDecay int       `json:"lastDecay"`
}

// SkillStatsFile holds persisted skill usage statistics.
type SkillStatsFile struct {
	mu        sync.Mutex                  `json:"-"`
	Version   int                         `json:"version"`
	TopN      int                         `json:"topN"`
	DecayStep int                         `json:"decayStep"` // total decay steps applied
	Entries   map[string]*SkillUsageEntry `json:"entries"`
	FilePath  string                      `json:"-"`
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
		Version   int                         `json:"version"`
		TopN      int                         `json:"topN"`
		DecayStep int                         `json:"decayStep"`
		Entries   map[string]*SkillUsageEntry `json:"entries"`
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
	s.DecayStep = file.DecayStep
	if file.Entries != nil {
		s.Entries = file.Entries
	}

	return s
}

// DecayForNewSession records one decay step. Call it exactly once per agent
// session start. Time is measured in agent sessions, not wall-clock time:
// while the agent is idle, scores do not decay. The step is applied lazily
// when scores are read or merged, so it composes with Save's merge with the
// on-disk copy (see mergeWithDisk).
func (s *SkillStatsFile) DecayForNewSession() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.DecayStep++
}

// decayedScore returns entry.Score expressed at step s: the stored value
// multiplied by the per-session decay factor for every step since the entry
// was last refreshed (LastDecay).
func decayedScore(entry *SkillUsageEntry, s int) float64 {
	n := s - entry.LastDecay
	if n <= 0 {
		return entry.Score
	}
	return entry.Score * math.Pow(sessionDecayFactor, float64(n))
}

// RecordUsage records one use of skillName: Count is incremented and Score
// gains +1 on top of the session-decayed value.
func (s *SkillStatsFile) RecordUsage(skillName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.Entries[skillName]
	if !ok {
		entry = &SkillUsageEntry{Name: skillName, LastDecay: s.DecayStep}
		s.Entries[skillName] = entry
	}

	entry.Count++
	entry.LastUsed = time.Now()
	entry.Score = decayedScore(entry, s.DecayStep) + 1
	entry.LastDecay = s.DecayStep
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
			entry = &SkillUsageEntry{Name: name, LastDecay: s.DecayStep}
			s.Entries[name] = entry
		}
		entry.LastShown = now
		entry.Score = decayedScore(entry, s.DecayStep)
		entry.LastDecay = s.DecayStep
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

// mergeWithDisk folds the on-disk copy into s before Save so concurrent
// agent processes don't clobber each other's updates with a stale in-memory
// snapshot.
//
// Scores are compared at the reference step (the larger of the two files'
// DecayStep), where each side's entry is expressed as
// Score * factor^(refStep - LastDecay). The larger effective value wins and
// is stored anchored at the reference step, so an entry's effective value
// never decreases across saves (the merge is monotonic and convergent).
// Count/LastUsed/LastShown merge by plain max; entries are a union. A
// missing or unreadable file is ignored (fresh start).
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
	if existing.DecayStep > s.DecayStep {
		s.DecayStep = existing.DecayStep
	}
	refStep := s.DecayStep
	for name, e := range existing.Entries {
		ours, ok := s.Entries[name]
		if !ok {
			s.Entries[name] = e
			continue
		}
		// Express both sides at refStep; the larger effective value wins.
		// Storing it anchored at refStep (LastDecay = refStep) keeps the
		// entry's future decay consistent.
		if eff := decayedScore(e, refStep); eff > decayedScore(ours, refStep) {
			ours.Score = eff
			ours.LastDecay = refStep
		} else {
			ours.Score = decayedScore(ours, refStep)
			ours.LastDecay = refStep
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
			scores[i] = decayedScore(entry, s.DecayStep)
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
		candidates = append(candidates, scored{name: name, score: decayedScore(entry, s.DecayStep)})
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
