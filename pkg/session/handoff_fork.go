package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// ForkHandoffSession copies the entire session directory to a new session ID.
// This is the handoff-mode equivalent of ForkSessionFrom. The new session
// inherits all checkpoints, current.txt, meta.json, and the full history.
//
// Unlike ForkSessionFrom (which performs entry-level branching via the
// leafID/parent tree), this method performs a raw filesystem-level copy of
// every file under the source session directory. This preserves the complete
// checkpoint chain (cp_001 → cp_002 → …) and the handoff.md documents.
func (sm *SessionManager) ForkHandoffSession(source *Session, name, title string) (*Session, error) {
	if source == nil {
		return nil, fmt.Errorf("source session is nil")
	}
	if err := os.MkdirAll(sm.sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}

	sourceDir := source.GetDir()
	if sourceDir == "" {
		return nil, fmt.Errorf("source session has no directory")
	}

	// Read the source's meta to inherit ContextManagementMode.
	sourceID := filepath.Base(sourceDir)
	inheritedMode := ""
	if meta, err := sm.GetMeta(sourceID); err == nil {
		inheritedMode = meta.ContextManagementMode
	}

	// Generate new ID and destination directory.
	id := uuid.New().String()
	sessDir := sm.getSessionPath(id)

	// Create the destination directory.
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Copy the entire source directory tree (messages.jsonl, checkpoints/,
	// current.txt, meta.json, handoff.md files, etc.).
	if err := copyDir(sourceDir, sessDir); err != nil {
		_ = os.RemoveAll(sessDir)
		return nil, fmt.Errorf("failed to copy session directory: %w", err)
	}

	// Load the copied session so we can update its header.
	newSess, err := LoadSession(sessDir)
	if err != nil {
		_ = os.RemoveAll(sessDir)
		return nil, fmt.Errorf("failed to load forked session: %w", err)
	}

	// Set ParentSession pointer to the source session's messages.jsonl path.
	newSess.header.ParentSession = source.GetPath()
	if err := newSess.rewriteFile(); err != nil {
		_ = os.RemoveAll(sessDir)
		return nil, fmt.Errorf("failed to update session header: %w", err)
	}

	// Create new metadata with the new ID, name, title, and inherited mode.
	meta := &SessionMeta{
		ID:                    id,
		Name:                  name,
		Title:                 title,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		MessageCount:          len(newSess.GetMessages()),
		ContextManagementMode: inheritedMode,
	}
	if err := sm.saveMeta(id, meta); err != nil {
		_ = os.RemoveAll(sessDir)
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	return newSess, nil
}

// copyDir recursively copies all files and subdirectories from src to dst.
// dst is assumed to already exist. Symlinks are not specially handled — the
// session directory should only contain regular files and directories.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dstPath, err)
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("copy %s → %s: %w", srcPath, dstPath, err)
			}
		}
	}
	return nil
}

// copyFile copies a single regular file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
