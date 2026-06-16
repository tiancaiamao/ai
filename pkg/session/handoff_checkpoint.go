package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Handoff checkpoint directory utilities.
//
// A handoff-mode session stores conversation history in a chain of
// context-segment checkpoints under checkpoints/cp_NNN/. Each checkpoint has
// its own messages.jsonl (reusing the existing SessionEntry format) plus an
// optional handoff.md document. current.txt points to the active segment.
//
// The functions here are intentionally low-level directory/file utilities —
// they do NOT manage the in-memory Session struct. The caller (agent loop,
// resume path) is responsible for coordinating when checkpoints are created,
// populated, and switched.

const (
	// handoffCheckpointsDir is the sub-directory holding all handoff segments.
	handoffCheckpointsDir = "checkpoints"
	// handoffCurrentFile records the name of the active checkpoint segment.
	handoffCurrentFile = "current.txt"
	// handoffMessagesFile is the per-checkpoint message log.
	handoffMessagesFile = "messages.jsonl"
	// handoffDocumentFile is the per-checkpoint handoff document.
	handoffDocumentFile = "handoff.md"
)

// checkpointNamePattern matches checkpoint directory names like cp_001, cp_042.
var checkpointNamePattern = regexp.MustCompile(`^cp_(\d+)$`)

// handoffCheckpointDir returns the directory path for a named checkpoint.
func handoffCheckpointDir(sessionDir, checkpointName string) string {
	return filepath.Join(sessionDir, handoffCheckpointsDir, checkpointName)
}

// listExistingCheckpoints returns the sorted checkpoint directory names found
// under sessionDir/checkpoints/. Only directories matching cp_NNN are returned.
func listExistingCheckpoints(sessionDir string) ([]string, error) {
	cpDir := filepath.Join(sessionDir, handoffCheckpointsDir)
	entries, err := os.ReadDir(cpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if checkpointNamePattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// nextCheckpointName determines the next sequential checkpoint name based on
// existing checkpoint directories. Returns "cp_001" if none exist yet.
func nextCheckpointName(sessionDir string) (string, error) {
	names, err := listExistingCheckpoints(sessionDir)
	if err != nil {
		return "", fmt.Errorf("list checkpoints: %w", err)
	}
	maxNum := 0
	for _, name := range names {
		m := checkpointNamePattern.FindStringSubmatch(name)
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > maxNum {
			maxNum = n
		}
	}
	return fmt.Sprintf("cp_%03d", maxNum+1), nil
}

// newHandoffSessionHeader builds a SessionHeader for a checkpoint's
// messages.jsonl. The header records the parent checkpoint for chain traversal.
func newHandoffSessionHeader(parentCheckpoint string) SessionHeader {
	return SessionHeader{
		Type:             EntryTypeSession,
		Version:          CurrentSessionVersion,
		ID:               fmt.Sprintf("handoff-%d", time.Now().UnixNano()),
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		ParentCheckpoint: parentCheckpoint,
	}
}

// CreateHandoffCheckpoint creates a new checkpoint directory (cp_NNN) under
// sessionDir/checkpoints/ and initializes messages.jsonl with a SessionHeader.
// It does NOT update current.txt — that is done by SwitchCheckpoint after the
// checkpoint data has been fully written.
//
// Returns the checkpoint name (e.g. "cp_002").
func CreateHandoffCheckpoint(sessionDir, parentCheckpoint string) (string, error) {
	checkpointName, err := nextCheckpointName(sessionDir)
	if err != nil {
		return "", err
	}

	cpDir := handoffCheckpointDir(sessionDir, checkpointName)
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		return "", fmt.Errorf("create checkpoint dir %s: %w", cpDir, err)
	}

	header := newHandoffSessionHeader(parentCheckpoint)
	msgsPath := filepath.Join(cpDir, handoffMessagesFile)
	data, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(msgsPath, data, 0644); err != nil {
		return "", fmt.Errorf("write header to %s: %w", msgsPath, err)
	}

	return checkpointName, nil
}

// WriteHandoffMessages appends the given entries to the checkpoint's
// messages.jsonl, after the header line.
func WriteHandoffMessages(sessionDir, checkpointName string, entries []SessionEntry) error {
	msgsPath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), handoffMessagesFile)
	file, err := os.OpenFile(msgsPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", msgsPath, err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for i := range entries {
		if err := encoder.Encode(&entries[i]); err != nil {
			return fmt.Errorf("encode entry %d: %w", i, err)
		}
	}
	return file.Sync()
}

// WriteHandoffDocument writes handoff.md into the checkpoint directory with the
// given content.
func WriteHandoffDocument(sessionDir, checkpointName, content string) error {
	docPath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), handoffDocumentFile)
	if err := os.WriteFile(docPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write handoff doc %s: %w", docPath, err)
	}
	return nil
}

// SwitchCheckpoint atomically writes current.txt with the checkpoint name.
// It uses a write-temp + rename pattern to guarantee atomicity.
func SwitchCheckpoint(sessionDir, checkpointName string) error {
	tmpPath := filepath.Join(sessionDir, ".current.txt.tmp")
	if err := os.WriteFile(tmpPath, []byte(checkpointName), 0644); err != nil {
		return fmt.Errorf("write temp current.txt: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(sessionDir, handoffCurrentFile)); err != nil {
		return fmt.Errorf("rename current.txt: %w", err)
	}
	return nil
}

// GetCurrentCheckpoint reads current.txt and returns the checkpoint name.
// Returns ("", nil) if current.txt does not exist (compatibility case).
func GetCurrentCheckpoint(sessionDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, handoffCurrentFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read current.txt: %w", err)
	}
	return string(data), nil
}

// LoadHandoffCheckpointMessages reads checkpoints/<checkpointName>/messages.jsonl,
// parses SessionEntry lines, extracts messages from EntryTypeMessage entries,
// and returns them in order. The SessionHeader line (EntryTypeSession) is
// skipped.
func LoadHandoffCheckpointMessages(sessionDir, checkpointName string) ([]agentctx.AgentMessage, error) {
	msgsPath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), handoffMessagesFile)
	f, err := os.Open(msgsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", msgsPath, err)
	}
	defer f.Close()

	var messages []agentctx.AgentMessage
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		entry, err := decodeSessionEntry(line)
		if err != nil || entry == nil {
			// Skip header line (decodeSessionEntry returns nil for type="session")
			// and unparseable lines.
			continue
		}
		if entry.Type == EntryTypeMessage && entry.Message != nil {
			msg := *entry.Message
			msg.EntryID = entry.ID
			messages = append(messages, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", msgsPath, err)
	}
	return messages, nil
}

// IsHandoffSession returns true if sessionDir has both a checkpoints/ directory
// and a current.txt file. This distinguishes handoff-mode sessions from legacy
// sessions that use the old checkpoint_%05d recovery system.
func IsHandoffSession(sessionDir string) bool {
	if sessionDir == "" {
		return false
	}
	cpDir := filepath.Join(sessionDir, handoffCheckpointsDir)
	if info, err := os.Stat(cpDir); err != nil || !info.IsDir() {
		return false
	}
	curPath := filepath.Join(sessionDir, handoffCurrentFile)
	if info, err := os.Stat(curPath); err != nil || info.IsDir() {
		return false
	}
	return true
}

// InitHandoffSession initializes the checkpoint structure for a new handoff-mode
// session. It creates checkpoints/cp_001/ with a SessionHeader (ParentCheckpoint
// empty) and writes current.txt pointing to "cp_001".
//
// If the session is already initialized (IsHandoffSession returns true), this
// is a no-op.
func InitHandoffSession(sessionDir string) error {
	if IsHandoffSession(sessionDir) {
		return nil
	}

	// Create the first checkpoint.
	checkpointName, err := CreateHandoffCheckpoint(sessionDir, "")
	if err != nil {
		return fmt.Errorf("create initial checkpoint: %w", err)
	}

	// Point current.txt at it.
	if err := SwitchCheckpoint(sessionDir, checkpointName); err != nil {
		return fmt.Errorf("switch to initial checkpoint: %w", err)
	}

	return nil
}

// ReadHandoffCheckpointHeader reads the SessionHeader (first line) from the
// checkpoint's messages.jsonl. Returns an error if the file cannot be read or
// the header line is missing/malformed.
func ReadHandoffCheckpointHeader(sessionDir, checkpointName string) (*SessionHeader, error) {
	msgsPath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), handoffMessagesFile)
	f, err := os.Open(msgsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", msgsPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scan %s: %w", msgsPath, err)
		}
		return nil, fmt.Errorf("header line not found in %s", msgsPath)
	}

	line := scanner.Bytes()
	if len(line) == 0 {
		return nil, fmt.Errorf("empty header line in %s", msgsPath)
	}

	var header SessionHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}
	return &header, nil
}

// ReadRootMessagesAfter reads the root messages.jsonl and returns message
// entries whose RFC3339 timestamp is strictly after cutoffTimestamp. Non-message
// entries and entries at or before the cutoff are skipped. If the root file
// does not exist, returns (nil, nil).
func ReadRootMessagesAfter(sessionDir, cutoffTimestamp string) ([]agentctx.AgentMessage, error) {
	cutoff, err := time.Parse(time.RFC3339Nano, cutoffTimestamp)
	if err != nil {
		return nil, fmt.Errorf("parse cutoff timestamp %q: %w", cutoffTimestamp, err)
	}

	rootPath := filepath.Join(sessionDir, "messages.jsonl")
	f, err := os.Open(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open root messages.jsonl: %w", err)
	}
	defer f.Close()

	var messages []agentctx.AgentMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		entry, derr := decodeSessionEntry(line)
		if derr != nil || entry == nil {
			continue
		}
		if entry.Type != EntryTypeMessage || entry.Message == nil {
			continue
		}
		entryTime, terr := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if terr != nil {
			continue
		}
		if entryTime.After(cutoff) {
			msg := *entry.Message
			msg.EntryID = entry.ID
			messages = append(messages, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan root messages.jsonl: %w", err)
	}
	return messages, nil
}

// WriteHandoffAgentState writes agent_state.json into the checkpoint directory.
// This allows the resume path to restore AgentState (CWD, token counts, etc.)
// after a handoff checkpoint switch.
func WriteHandoffAgentState(sessionDir, checkpointName string, state *agentctx.AgentState) error {
	statePath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), "agent_state.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent state: %w", err)
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("write agent_state.json: %w", err)
	}
	return nil
}

// LoadHandoffCheckpointAgentState reads agent_state.json from the checkpoint
// directory. Returns (nil, nil) if the file does not exist (backward
// compatible with checkpoints created before agent state persistence).
func LoadHandoffCheckpointAgentState(sessionDir, checkpointName string) (*agentctx.AgentState, error) {
	statePath := filepath.Join(handoffCheckpointDir(sessionDir, checkpointName), "agent_state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent_state.json: %w", err)
	}
	var state agentctx.AgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal agent state: %w", err)
	}
	return &state, nil
}
