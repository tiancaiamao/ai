package tools

import (
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// sudo password support for the bash tool.
//
// Inspired by the approach in hermes-agent/tools/terminal_tool.py:
//
//   • Rewrites bare `sudo cmd` → `sudo -S -p '' cmd` so sudo reads the
//     password from stdin instead of /dev/tty.
//   • When SUDO_PASSWORD is set, the rewritten command gets the password
//     piped to its stdin.
//   • Without SUDO_PASSWORD, the command runs unchanged: sudo may still
//     succeed via an existing sudoers NOPASSWD rule or a still-valid
//     timestamp cache (e.g. the user recently typed the password in a
//     terminal). If none apply, sudo fails gracefully with a "password
//     required" error and the bash tool appends a helpful hint.
// ---------------------------------------------------------------------------

// sudoResult holds the result of transforming a sudo command.
type sudoResult struct {
	// command is the transformed command string (uses sudo -S -p '').
	command string

	// passwordLines is the password + "\n" to write to stdin, repeated for
	// each sudo invocation in a compound command. Empty if not needed.
	passwordLines string
}

// transformSudoCommand inspects a bash command for bare `sudo` invocations
// and returns a transformed version that uses sudo -S (stdin password read).
//
// Decision tree:
//
//  1. No sudo invocations → return command unchanged, no password.
//  2. SUDO_PASSWORD env var is set → rewrite to sudo -S, return password.
//  3. Otherwise → return command unchanged. sudo may still succeed via a
//     NOPASSWD sudoers rule or a valid timestamp cache; if not, it fails
//     gracefully (no rewrite, no password) and the bash tool appends a hint.
func transformSudoCommand(command string) sudoResult {
	if command == "" {
		return sudoResult{command: command}
	}

	transformed, sudoCount := rewriteSudoInvocations(command)
	if sudoCount == 0 {
		return sudoResult{command: command}
	}

	// SUDO_PASSWORD env var (explicit, persistent)
	if pwd := os.Getenv("SUDO_PASSWORD"); pwd != "" {
		return sudoResult{
			command:       transformed,
			passwordLines: strings.Repeat(pwd+"\n", sudoCount),
		}
	}

	// No password available — run unchanged. sudo may still succeed via a
	// NOPASSWD sudoers rule or a valid timestamp cache (we must not probe
	// with `sudo -n` here: its result depends on the timestamp cache and
	// probing with `sudo -k` would clear a user's still-valid cache).
	// If no rule/cache applies, sudo fails gracefully and SudoHint explains
	// how to enable passwordless sudo.
	return sudoResult{command: command}
}

// ---------------------------------------------------------------------------
// Shell token parser used by rewriteSudoInvocations.
// ---------------------------------------------------------------------------

// readShellToken reads the next shell token from command starting at start.
// Handles:
//   - unquoted words (stop at space, tab, newline, ; | & ( ))
//   - single-quoted strings (preserve content literally)
//   - double-quoted strings (preserve content with \ escaping)
//   - $( subshells (track nesting depth of $() structures)
func readShellToken(command string, start int) (token string, next int) {
	n := len(command)
	if start >= n {
		return "", start
	}

	// Single-quoted string
	if command[start] == '\'' {
		var b strings.Builder
		b.WriteByte('\'')
		idx := start + 1
		for idx < n {
			c := command[idx]
			b.WriteByte(c)
			if c == '\'' {
				idx++
				return b.String(), idx
			}
			idx++
		}
		return b.String(), idx
	}

	// Double-quoted string
	if command[start] == '"' {
		var b strings.Builder
		b.WriteByte('"')
		idx := start + 1
		for idx < n {
			c := command[idx]
			b.WriteByte(c)
			if c == '"' {
				idx++
				return b.String(), idx
			}
			if c == '\\' && idx+1 < n {
				idx++
				b.WriteByte(command[idx])
			}
			idx++
		}
		return b.String(), idx
	}

	// $( subshell — read until matching ), respecting nested $() and quotes
	if start+1 < n && command[start] == '$' && command[start+1] == '(' {
		var b strings.Builder
		b.WriteString("$(")
		depth := 1
		idx := start + 2
		for idx < n && depth > 0 {
			c := command[idx]
			b.WriteByte(c)
			switch {
			case c == '(':
				depth++
			case c == ')':
				depth--
			case c == '\'' || c == '"':
				// Skip to end of quote
				quote := c
				for idx+1 < n {
					idx++
					c = command[idx]
					b.WriteByte(c)
					if c == quote {
						break
					}
					if c == '\\' && idx+1 < n {
						idx++
						b.WriteByte(command[idx])
					}
				}
			}
			idx++
		}
		return b.String(), idx
	}

	// Regular word
	var b strings.Builder
	for idx := start; idx < n; idx++ {
		c := command[idx]
		if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '|' || c == '&' || c == '(' || c == ')' {
			break
		}
		b.WriteByte(c)
	}
	return b.String(), start + b.Len()
}

// rewriteSudoInvocations rewrites bare `sudo` tokens to `sudo -S -p ”`
// and returns the transformed command plus the count of rewritten invocations.
//
// It only rewrites real unquoted sudo command words appearing at command
// boundaries (start of command, after ; | & && || \n, or after ().
// Sudo inside quotes, comments, or as part of another word is left alone.
func rewriteSudoInvocations(command string) (string, int) {
	var out strings.Builder
	var sudoCount int
	i := 0
	n := len(command)
	commandStart := true
	afterPipe := false

	for i < n {
		ch := command[i]

		// Skip spaces and tabs
		if ch == ' ' || ch == '\t' {
			out.WriteByte(ch)
			i++
			continue
		}

		// Newlines reset commandStart and afterPipe
		if ch == '\n' {
			out.WriteByte(ch)
			i++
			commandStart = true
			afterPipe = false
			continue
		}

		// Comments
		if ch == '#' && commandStart {
			out.WriteString(command[i:])
			break
		}

		// Combined operators: &&, ||, ;;
		if i+1 < n {
			next := command[i+1]
			if (ch == '&' && next == '&') || (ch == '|' && next == '|') || (ch == ';' && next == ';') {
				out.WriteByte(ch)
				out.WriteByte(next)
				i += 2
				commandStart = true
				afterPipe = false
				continue
			}
		}

		// Single-char separators: ; | &
		if ch == ';' || ch == '|' || ch == '&' {
			out.WriteByte(ch)
			i++
			commandStart = true
			afterPipe = (ch == '|')
			continue
		}

		// ( starts a new command context
		if ch == '(' {
			out.WriteByte(ch)
			i++
			commandStart = true
			afterPipe = false
			continue
		}

		// ) ends a subshell — not a command boundary
		if ch == ')' {
			out.WriteByte(ch)
			i++
			commandStart = false
			afterPipe = false
			continue
		}

		// Read a full token (handles quotes and $() subshells)
		token, next := readShellToken(command, i)
		if commandStart && token == "sudo" && !afterPipe {
			out.WriteString("sudo -S -p ''")
			sudoCount++
		} else {
			out.WriteString(token)
		}
		commandStart = false
		afterPipe = false
		i = next
	}

	return out.String(), sudoCount
}

// ---------------------------------------------------------------------------
// sudo failure detection
// ---------------------------------------------------------------------------

// sudoFailureMarkers are error messages printed by sudo when it needs
// a password but can't get one.
var sudoFailureMarkers = []string{
	"sudo: a password is required",
	"sudo: no tty present",
	"sudo: a terminal is required",
}

// hasSudoFailure checks if the output contains a sudo password-required
// error.
func hasSudoFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range sudoFailureMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// SudoHint returns a hint string about configuring SUDO_PASSWORD when
// sudo fails in a non-interactive context. Returns empty string if the
// output doesn't indicate a sudo failure.
func SudoHint(output string) string {
	if !hasSudoFailure(output) {
		return ""
	}
	return "\n\n💡 Tip: To enable sudo without interactive prompts, add SUDO_PASSWORD to your .env file or export it in your shell profile."
}
