# pkg/execworld

Execution world abstraction: the place where tool operations (read/write/bash/
grep/stat) run. The default world is the local host; `SSHWorld` is a remote
world reached through the system `ssh` client.

## World interface

```go
type World interface {
    ReadFile(ctx, path) ([]byte, error)
    Stat(ctx, path) (FileInfo, error)
    Run(ctx, spec RunSpec) (RunResult, error)
    WriteFile(ctx, path, data, perm) error
    CommandExists(ctx, name) bool
}
```

## SSHWorld

- Target syntax: `user@host` or `user@host:/path` (sets the remote start cwd).
- Each operation spawns a fresh `ssh` process; a ControlMaster socket
  (`~/.ai`-free, uses the system temp dir) amortizes the auth/TCP cost.
- Command text travels over **stdin** (`cd <cwd> && exec bash -s`), never on
  the remote command line, so arbitrary agent commands need no shell escaping.
- File content travels over stdin into remote `cat > path`; only the path is
  shell-quoted.
- Password auth: `AI_SSH_PASSWORD` env + OpenSSH `SSH_ASKPASS` mechanism
  (`SSH_ASKPASS_REQUIRE=force`). Enable it by setting the env var; passwordless
  key auth keeps `BatchMode=yes` (no interactive prompts ever).
- `~` in paths is expanded against the **remote** home (`$HOME`), never the
  local one.
- `CommandExists` probes the remote PATH (`command -v`), e.g. grep's `rg`
  detection.

## Wiring

`createWorkspaceAndRegistry` (pkg/rpc) builds the workspace against
`world.InitialCwd()` when `--ssh user@host[:path]` is set; tools (bash/read/
grep/write/edit) take the world branch. Off the remote path the workspace and
tools are untouched — local behavior is identical (all existing tests pass
unchanged).