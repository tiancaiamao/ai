package execworld

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHWorld forwards primitive operations to a remote host via the system
// ssh client. Each operation spawns a fresh ssh process; ControlMaster connection
// reuse keeps the authentication/TCP cost amortized across operations.
type SSHWorld struct {
	target      string // "user@host"
	initialCwd  string // remote starting cwd (user home unless overridden)
	homeDir     string // remote $HOME (cached at connect time)
	controlPath string // local ControlMaster socket path (shared per target)
}

// NewSSHWorld connects to target, probes the remote environment, and returns
// a ready SSHWorld.
//
// Target syntax: "user@host" or "user@host:/absolute/path" (the latter sets
// the remote starting cwd). ProxyJump and key configuration are inherited
// transparently from ~/.ssh/config.
func NewSSHWorld(target string) (*SSHWorld, error) {
	if !strings.Contains(target, "@") {
		return nil, fmt.Errorf("invalid ssh target %q: expected user@host or user@host:/path", target)
	}

	hostPart, startDir := target, ""
	if i := strings.Index(target, ":"); i >= 0 {
		hostPart, startDir = target[:i], target[i+1:]
		if startDir == "" {
			return nil, fmt.Errorf("invalid ssh target %q: empty path after ':'", target)
		}
	}

	// ControlMaster socket path, stable per target so a series of operations
	// after a connect share one master connection.
	sum := sha256.Sum256([]byte(hostPart))
	controlPath := filepath.Join(os.TempDir(), fmt.Sprintf("ai-ssh-%s.sock", hex.EncodeToString(sum[:6])))

	w := &SSHWorld{
		target:      hostPart,
		initialCwd:  startDir,
		controlPath: controlPath,
	}

	// Probe the remote environment.
	home, err := w.probeHome()
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed (target %s): %w", hostPart, err)
	}
	w.homeDir = home
	if w.initialCwd == "" {
		w.initialCwd = home
	}
	return w, nil
}

// InitialCwd returns the remote directory commands start in.
func (w *SSHWorld) InitialCwd() string { return w.initialCwd }

// Host returns the "user@host" target.
func (w *SSHWorld) Host() string { return w.target }

// Run implements World.Run. The command text travels over stdin (never through
// the remote shell's command line), so arbitrary agent commands need no
// quoting/escaping. The remote side is `cd <cwd> && exec bash -s`.
func (w *SSHWorld) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	cmdText := spec.Command
	if cmdText == "" && len(spec.Argv) > 0 {
		quoted := make([]string, len(spec.Argv))
		for i, a := range spec.Argv {
			// expandHome first: the quoted form would otherwise hide "~"
			// from the remote shell (single quotes prevent expansion).
			quoted[i] = shellQuote(w.expandHome(a))
		}
		cmdText = strings.Join(quoted, " ")
	}
	if cmdText == "" {
		return RunResult{ExitCode: 2}, fmt.Errorf("empty command")
	}

	remoteCmd := "exec bash -s"
	if spec.Cwd != "" {
		remoteCmd = "cd " + shellQuote(spec.Cwd) + " && exec bash -s"
	}
	return w.sshCommand(ctx, remoteCmd, cmdText)
}

// ReadFile implements World.ReadFile.
func (w *SSHWorld) ReadFile(ctx context.Context, path string) ([]byte, error) {
	script := "cat -- " + shellQuote(w.expandHome(path))
	res, err := w.sshCommand(ctx, "exec bash -s", script)
	if err != nil {
		return nil, fmt.Errorf("remote read failed: %w", err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return nil, fmt.Errorf("remote read failed (exit %d): %s", res.ExitCode, msg)
	}
	return []byte(res.Stdout), nil
}

// Stat implements World.Stat.
func (w *SSHWorld) Stat(ctx context.Context, path string) (FileInfo, error) {
	p := w.expandHome(path)
	script := fmt.Sprintf(
		`p=%s; if [ -d "$p" ]; then echo DIR; elif [ -f "$p" ]; then echo FILE; elif [ -e "$p" ]; then echo OTHER; else echo MISSING; fi`,
		shellQuote(p))
	res, err := w.sshCommand(ctx, "exec bash -s", script)
	if err != nil {
		return FileInfo{}, err
	}
	kind := strings.TrimSpace(res.Stdout)
	return FileInfo{
		Name:   filepath.Base(p),
		Exists: kind != "MISSING",
		IsDir:  kind == "DIR",
	}, nil
}

// expandHome rewrites a leading "~/" to the remote home directory. The remote
// shell would expand "~" itself, but the path may be wrapped in single quotes
// (where the shell does not expand), so the SSHWorld does it explicitly.
func (w *SSHWorld) expandHome(path string) string {
	if path == "~" {
		return w.homeDir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(w.homeDir, path[2:])
	}
	return path
}

// probeHome returns the remote $HOME by running a trivial command.
func (w *SSHWorld) probeHome() (string, error) {
	res, err := w.sshCommand(context.Background(), "exec bash -s", "echo \"$HOME\"")
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(res.Stdout)
	if home == "" {
		return "", fmt.Errorf("could not determine remote $HOME (stderr: %s)", strings.TrimSpace(res.Stderr))
	}
	return home, nil
}

// sshCommand spawns one ssh invocation. The command to run remotely is passed
// as an argument; the script to feed to the remote `bash -s` (if any) travels
// via stdin.
func (w *SSHWorld) sshCommand(ctx context.Context, remoteCmd, stdinText string) (RunResult, error) {
	args := w.baseSSHArgs()
	args = append(args, remoteCmd)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	if stdinText != "" {
		cmd.Stdin = strings.NewReader(stdinText)
	}
	if pw := os.Getenv("AI_SSH_PASSWORD"); pw != "" {
		// Password authentication without a TTY: hand the password to ssh via
		// the OpenSSH askpass protocol. The script itself only echoes the
		// password from the environment, so the credential is not written to
		// disk. SSH_ASKPASS_REQUIRE=force makes ssh call it even without a
		// controlling terminal.
		askpassPath, err := w.writeAskpassScript()
		if err != nil {
			return RunResult{}, fmt.Errorf("write askpass script: %w", err)
		}
		defer func() {
			// Best-effort cleanup; SIGHUP delivery order makes racy removal
			// harmless (a leftover script contains no secret).
			os.Remove(askpassPath)
		}()
		cmd.Env = append(os.Environ(),
			"SSH_ASKPASS="+askpassPath,
			"SSH_ASKPASS_REQUIRE=force",
		)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	res := RunResult{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		ExitCode: 0,
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, err
		}
	}
	return res, nil
}

// baseSSHArgs returns the ssh invocation shared by every operation.
// When AI_SSH_PASSWORD is set the ssh client is not forced into batch mode so
// password authentication can proceed via SSH_ASKPASS (see sshCommand); the
// flag is removed only in that case.
func (w *SSHWorld) baseSSHArgs() []string {
	args := []string{
		w.target,
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + w.controlPath,
		"-o", "ControlPersist=300",
		"-o", "ConnectTimeout=10",
	}
	if os.Getenv("AI_SSH_PASSWORD") == "" {
		args = append(args, "-o", "BatchMode=yes")
	}
	return args
}

// writeAskpassScript creates the helper script used by ssh for password
// prompts. The script echoes the password from the environment; it contains no
// credential material itself.
func (w *SSHWorld) writeAskpassScript() (string, error) {
	path := filepath.Join(os.TempDir(), "ai-askpass-"+filepath.Base(w.controlPath)+".sh")
	content := "#!/bin/sh\nprintf '%s' \"$AI_SSH_PASSWORD\"\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// WriteFile implements World.WriteFile. The file content travels over ssh
// stdin straight into a remote `cat > path`, so no shell escaping of the
// content is ever needed; only the path is quoted.
func (w *SSHWorld) WriteFile(ctx context.Context, path string, data []byte, perm os.FileMode) error {
	p := w.expandHome(path)
	dir := filepath.Dir(p)
	if dir != "" && dir != "." {
		res, err := w.sshCommand(ctx, "exec bash -s", "mkdir -p "+shellQuote(dir))
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("remote mkdir failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	}
	res, err := w.sshCommand(ctx, "cat > "+shellQuote(p), string(data))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("remote write failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	if perm != 0 {
		if _, err := w.sshCommand(ctx, "exec bash -s",
			"chmod "+strconv.FormatUint(uint64(perm.Perm()), 8)+" "+shellQuote(p)); err != nil {
			return err
		}
	}
	return nil
}

// CommandExists implements World.CommandExists by probing the remote PATH.
func (w *SSHWorld) CommandExists(ctx context.Context, name string) bool {
	res, err := w.sshCommand(ctx, "exec bash -s", "command -v "+shellQuote(name))
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
}

// shellQuote wraps s in single quotes, escaping embedded single quotes the
// classic '\” way. Used for values that travel on the remote command line
// (cwd and argv forms), never for whole agent commands.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
