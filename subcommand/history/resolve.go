package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// resolveSessionDir maps the CLI addressing flags to a session directory.
//
// Resolution order (docs/design-history-cli.md §4.2):
//  1. --session <path>: explicit escape hatch, used as-is (typically when an
//     old run.json carries no Session field).
//  2. --id <run-id|prefix> via helpers.ResolveRunIDForHistory (required;
//     matches running and finished runs), then read run.json's Session field
//     (the session UUID) and locate it under the run's session directory.
//
// There is no cwd-based fallback on purpose: an agent's working directory can
// change during its lifetime, so the cwd does not reliably identify the run.
func resolveSessionDir(baseDir, idFlag, sessionFlag string) (string, error) {
	if sessionFlag != "" {
		info, err := os.Stat(sessionFlag)
		if err != nil {
			return "", fmt.Errorf("--session %s: %w", sessionFlag, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("--session %s is not a session directory", sessionFlag)
		}
		return sessionFlag, nil
	}

	meta, err := helpers.ResolveRunIDForHistory(baseDir, idFlag)
	if err != nil {
		return "", disambiguationError(baseDir, idFlag, err)
	}
	if meta.Session == "" {
		return "", fmt.Errorf("run %s does not record a session; use --session <path> to specify the session directory", meta.ID)
	}
	return sessionDirForRun(meta)
}

// maxDisambiguationCandidates bounds the candidate list in the prefix
// ambiguity error; a very short prefix can match hundreds of runs.
const maxDisambiguationCandidates = 10

// disambiguationError enriches resolution failures with candidate IDs.
// Prefix ambiguity must never be silently guessed: when a prefix matches
// multiple runs, candidate IDs are listed — bounded to
// maxDisambiguationCandidates so a short prefix cannot flood the output.
func disambiguationError(baseDir, idFlag string, err error) error {
	if idFlag == "" {
		return err
	}
	matches, _ := tui.FindByPrefix(baseDir, idFlag)
	if len(matches) <= 1 {
		return err
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	if len(ids) > maxDisambiguationCandidates {
		return fmt.Errorf("run ID prefix %q is ambiguous (%d candidates: %s ...), use a longer --id",
			idFlag, len(ids), strings.Join(ids[:maxDisambiguationCandidates], " "))
	}
	return fmt.Errorf("run ID prefix %q is ambiguous, candidates: %v; use a longer --id", idFlag, ids)
}

// sessionDirForRun locates the session directory for a run. RunMeta.Session
// stores the session UUID (relative to the sessions dir derived from the
// run's recorded cwd); a path may also be stored for manually patched runs.
func sessionDirForRun(meta *tui.RunMeta) (string, error) {
	if strings.ContainsRune(meta.Session, os.PathSeparator) {
		if info, err := os.Stat(meta.Session); err == nil && info.IsDir() {
			return meta.Session, nil
		}
	}

	cwd := meta.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get cwd: %w", err)
		}
	}
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve sessions dir for run %s: %w", meta.ID, err)
	}
	dir := filepath.Join(sessionsDir, filepath.Base(meta.Session))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("session %q for run %s not found under %s", meta.Session, meta.ID, sessionsDir)
	}
	return dir, nil
}

// loadTarget resolves the session directory and loads the session in full
// readiness for queries (the query layer forces lazy sessions to load).
func loadTarget(baseDir, idFlag, sessionFlag string) (*session.Session, error) {
	dir, err := resolveSessionDir(baseDir, idFlag, sessionFlag)
	if err != nil {
		return nil, err
	}
	sess, err := session.LoadSession(dir)
	if err != nil {
		return nil, fmt.Errorf("load session %s: %w", dir, err)
	}
	return sess, nil
}
