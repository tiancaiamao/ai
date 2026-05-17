# pkg/version

VCS revision extraction from Go build information.

## Overview

Extracts the Git commit hash from the compiled binary using `runtime/debug.ReadBuildInfo()`. No build flags required — Go automatically embeds VCS metadata when building from a Git repository.

## API

```go
var GitCommit string  // e.g., "f5fa7e9" or "f5fa7e9-dirty"
var GitVersion string // Reserved for future use
```

The `-dirty` suffix is appended when the working tree had uncommitted changes at build time.

## Key Files

| File | Description |
|------|-------------|
| `version.go` | `init()` function extracting VCS revision from `debug.BuildInfo` |