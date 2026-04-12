package version

import "runtime/debug"

var (
	// GitCommit is the VCS revision of the ai binary, with "-dirty" suffix
	// if the working tree had uncommitted changes at build time.
	GitCommit string
	// GitVersion is not currently populated (reserved for future use with ldflags).
	GitVersion string
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var revision, modified string
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			revision = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value
		}
	}
	GitCommit = revision
	if modified == "true" {
		GitCommit += "-dirty"
	}
}
