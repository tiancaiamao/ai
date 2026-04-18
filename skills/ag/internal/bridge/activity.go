package bridge

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genius/ag/internal/storage"
)

const (
	activityFileName     = "activity.json"
	textRateLimitInterval = 2 * time.Second
)

// ActivityWriter manages writes to activity.json in an agent directory.
// It is thread-safe and rate-limits text_delta updates to at most once per 2 seconds.
type ActivityWriter struct {
	mu        sync.Mutex
	agentDir  string
	activity  AgentActivity
	lastWrite time.Time
	lastText  string // snapshot of LastText after last write
	closed    bool
}

// NewActivityWriter creates a new ActivityWriter for the given agent directory.
// If an existing activity.json is present, it is loaded (resume case).
func NewActivityWriter(agentDir string) *ActivityWriter {
	aw := &ActivityWriter{
		agentDir: agentDir,
	}

	// Attempt to resume from existing activity.json
	path := filepath.Join(agentDir, activityFileName)
	var existing AgentActivity
	if err := storage.ReadJSON(path, &existing); err == nil {
		aw.activity = existing
		aw.lastText = existing.LastText
		aw.lastWrite = time.Now()
	}

	return aw
}

// UpdateStatus sets the status and writes immediately.
func (aw *ActivityWriter) UpdateStatus(status AgentStatus) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	if aw.closed {
		return
	}
	aw.activity.Status = status
	if status == StatusDone || status == StatusFailed || status == StatusKilled {
		aw.activity.FinishedAt = time.Now().Unix()
	}
	aw.writeNow()
}

// UpdateActivity applies a mutation function to the activity and then writes.
// Rate limiting is applied: if only LastText changed and the last write was
// less than 2 seconds ago, the write is skipped.
func (aw *ActivityWriter) UpdateActivity(fn func(*AgentActivity)) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	if aw.closed {
		return
	}

	// Snapshot LastText before mutation to detect text-only changes
	prevText := aw.activity.LastText

	fn(&aw.activity)

	// Determine if only LastText changed
	onlyTextChanged := aw.activity.LastText != prevText &&
		aw.activity.Status != StatusDone &&
		aw.activity.Status != StatusFailed &&
		aw.activity.Status != StatusKilled

	// Check if status is a terminal one (always write immediately)
	status := aw.activity.Status
	isTerminal := status == StatusDone || status == StatusFailed || status == StatusKilled

	if onlyTextChanged && !isTerminal {
		// Rate-limited: skip write if too recent
		if time.Since(aw.lastWrite) < textRateLimitInterval {
			return
		}
	}

	aw.writeNow()
}

// SetError sets the Error field and status to StatusFailed, then writes immediately.
func (aw *ActivityWriter) SetError(errMsg string) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	if aw.closed {
		return
	}
	aw.activity.Error = errMsg
	aw.activity.Status = StatusFailed
	aw.activity.FinishedAt = time.Now().Unix()
	aw.writeNow()
}

// Close performs a final flush of any pending writes and marks the writer as closed.
func (aw *ActivityWriter) Close() {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	if aw.closed {
		return
	}
	// Final flush: write even if only text changed
	aw.writeNow()
	aw.closed = true
}

// writeNow writes the current activity to activity.json.
// Must be called with aw.mu held.
func (aw *ActivityWriter) writeNow() {
	// Ensure parent directory exists
	if err := os.MkdirAll(aw.agentDir, 0755); err != nil {
		return
	}

	path := filepath.Join(aw.agentDir, activityFileName)
	_ = storage.AtomicWriteJSON(path, &aw.activity)

	aw.lastWrite = time.Now()
	aw.lastText = aw.activity.LastText
}