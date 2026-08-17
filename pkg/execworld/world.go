// Package execworld abstracts the environment where the agent's tools execute.
//
// The local implementation (the zero value / not using this package) operates
// directly on the host filesystem. The SSH implementation forwards primitive
// operations (read file, stat, run command) to a remote host through the system
// ssh client with ControlMaster connection reuse.
//
// This is a minimal prototype surface: only the primitives needed by the
// bash / read / grep tools are exposed.
package execworld

import (
	"context"
	"os"
)

// FileInfo is a minimal file metadata result.
type FileInfo struct {
	Name   string
	Size   int64
	IsDir  bool
	Exists bool
}

// RunSpec describes a command to execute in the target world.
type RunSpec struct {
	// Command is the full shell command text (used by the bash tool).
	// When empty, Argv is used instead.
	Command string
	// Argv is the argv form (used by the grep tool). Command takes
	// precedence when both are set.
	Argv []string
	// Cwd is the working directory on the target. Empty means "no cd".
	Cwd string
}

// RunResult is the outcome of a Run call. Output is fully collected (the
// tools never consume it as a stream).
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// World is the primitive surface the tools operate against.
// A nil World means "local host" — the tools keep their existing direct
// os/exec implementation in that case.
type World interface {
	// ReadFile returns the raw bytes of a file.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// Stat returns metadata for a path.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// Run executes a command and collects all output.
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
	// CommandExists reports whether an executable is available in the target
	// environment (used for tool planning, e.g. rg vs grep fallback).
	CommandExists(ctx context.Context, name string) bool
	// WriteFile writes raw bytes to a path, creating parent directories as
	// needed. perm is honored when the target supports it (best effort).
	WriteFile(ctx context.Context, path string, data []byte, perm os.FileMode) error
}

// Compile-time check that SSHWorld implements World.
var _ World = (*SSHWorld)(nil)
