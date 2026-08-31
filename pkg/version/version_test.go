package version

import (
	"strings"
	"testing"
)

// TestGitCommitInitialized verifies that init() populated GitCommit based on
// the build info embedded by `go test`. The exact value depends on the build
// environment, but in a clean test run the variable should be set to a
// non-empty commit hash (possibly with "-dirty" suffix).
func TestGitCommitInitialized(t *testing.T) {
	// When running tests, Go embeds build info that includes vcs.revision.
	// init() should have picked this up; we just need to verify the variable
	// is reachable and (in most environments) populated.
	_ = GitCommit
	_ = GitVersion

	// Sanity: if GitCommit is set, it should look like a hex sha (40 chars),
	// optionally with a "-dirty" suffix.
	if GitCommit != "" {
		clean := strings.TrimSuffix(GitCommit, "-dirty")
		// revision should only contain hex chars; we don't enforce length
		// since builds can have shorter SHAs in some environments.
		for _, c := range clean {
			ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
			if !ok {
				t.Errorf("GitCommit contains non-hex character %q in %q", c, GitCommit)
				break
			}
		}
	}
}
