package tools

import (
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// sudo password support for the bash tool.
//
// Inspired by the approach in hermes-agent/tools/terminal_tool.py.
//
// Bare `sudo` invocations are rewritten so they can never hang waiting
// for a password on a non-tty stdin. Two modes, chosen by SUDO_PASSWORD:
//
//   • SUDO_PASSWORD set → rewrite `sudo cmd` → `sudo -S -p '' cmd` and
//     pipe the password to stdin. Every sudo invocation goes through.
//   • SUDO_PASSWORD unset → rewrite `sudo cmd` → `sudo -n cmd`. sudo
//     still succeeds via a NOPASSWD sudoers rule or a still-valid
//     timestamp cache (e.g. the user recently typed the password in a
//     terminal); otherwise it fails immediately instead of blocking.
//     Lower bound: never hang.
// ---------------------------------------------------------------------------

// sudoResult holds the result of transforming a sudo command.
type sudoResult struct {
	// command is the transformed command string.
	command string

	// passwordLines is the password + "\n" to write to stdin, repeated for
	// each sudo invocation in a compound command. Empty if not needed.
	passwordLines string
}

// sudo rewrite modes used with rewriteSudoInvocations.
const (
	// sudoStdinRewrite reads the password from stdin (needs SUDO_PASSWORD).
	sudoStdinRewrite = "sudo -S -p ''"
	// sudoNonInteractiveRewrite never prompts; fails fast when a password
	// would be required (works via NOPASSWD rule or valid timestamp cache).
	sudoNonInteractiveRewrite = "sudo -n"
)

// transformSudoCommand inspects a bash command for bare `sudo` invocations
// and returns a transformed version that can never block on a password
// prompt.
//
// Decision tree:
//
//  1. No sudo invocations → return command unchanged, no password.
//  2. SUDO_PASSWORD env var is set → rewrite to `sudo -S -p ”`, return
//     the password (repeated per invocation) for stdin injection.
//  3. Otherwise → rewrite to `sudo -n` so sudo can't block waiting for
//     input: it succeeds via a NOPASSWD rule or a still-valid timestamp
//     cache, and fails immediately otherwise (SudoHint explains how to
//     enable passwordless sudo).
func transformSudoCommand(command string) sudoResult {
	if command == "" {
		return sudoResult{command: command}
	}

	// SUDO_PASSWORD set — every sudo invocation goes through.
	if pwd := os.Getenv("SUDO_PASSWORD"); pwd != "" {
		transformed, sudoCount := rewriteSudoInvocations(command, sudoStdinRewrite)
		if sudoCount == 0 {
			return sudoResult{command: command}
		}
		return sudoResult{
			command:       transformed,
			passwordLines: strings.Repeat(pwd+"\n", sudoCount),
		}
	}

	// No SUDO_PASSWORD — lower bound: never hang. `sudo -n` fails fast
	// instead of blocking on a non-tty stdin, while still succeeding via
	// a NOPASSWD rule or a still-valid timestamp cache. We must not probe
	// with `sudo -k` here: it would clear a user's still-valid cache.
	transformed, sudoCount := rewriteSudoInvocations(command, sudoNonInteractiveRewrite)
	if sudoCount == 0 {
		return sudoResult{command: command}
	}
	return sudoResult{command: transformed}
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

// rewriteSudoInvocations rewrites bare `sudo` tokens to replacement
// (e.g. "sudo -S -p ”" or "sudo -n") and returns the transformed command
// plus the count of rewritten invocations.
//
// It only rewrites real unquoted sudo command words appearing at command
// boundaries (start of command, after ; | & && || \n, or after ().
// Sudo inside quotes, comments, or as part of another word is left alone.
func rewriteSudoInvocations(command, replacement string) (string, int) {
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
			out.WriteString(replacement)
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
	"sudo: interactive authentication is required",
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
