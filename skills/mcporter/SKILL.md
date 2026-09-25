---
name: mcp
description: Access MCP servers/tools from the agent (list, schema, call, auth, config, codegen). Current implementation: the mcporter CLI.
homepage: http://mcporter.dev
metadata:
  {
    "goclaw":
      {
        "emoji": "📦",
        "requires": { "bins": ["mcporter"] },
        "install":
          [
            {
              "id": "node",
              "kind": "node",
              "package": "mcporter",
              "bins": ["mcporter"],
              "label": "Install mcporter (node)",
            },
          ],
      },
  }
---

# mcp

The agent has no built-in MCP protocol support. This skill is the interface for MCP access; the current implementation is the `mcporter` CLI. If the implementation changes, keep the interface stable: list servers, inspect schemas, call `server.tool` with args, and handle large JSON outputs.

Current implementation: mcporter

## Quick start

- `mcporter list`
- `mcporter list <server> --schema`
- `mcporter call <server.tool> key=value`

## Call tools

- Selector: `mcporter call linear.list_issues team=ENG limit:5`
- Function syntax: `mcporter call "linear.create_issue(title: \"Bug\")"`
- Full URL: `mcporter call https://api.example.com/mcp.fetch url:https://example.com`
- Stdio: `mcporter call --stdio "bun run ./server.ts" scrape url=https://example.com`
- JSON payload: `mcporter call <server.tool> --args '{"limit":5}'`

## Auth + config

- OAuth: `mcporter auth <server | url> [--reset]`
- Config: `mcporter config list|get|add|remove|import|login|logout`

## Daemon

- `mcporter daemon start|status|stop|restart`

## Codegen

- CLI: `mcporter generate-cli --server <name>` or `--command <url>`
- Inspect: `mcporter inspect-cli <path> [--json]`
- TS: `mcporter emit-ts <server> --mode client|types`

## Notes

- Config default: `./config/mcporter.json` (override with `--config`).
- Prefer `--output json` for machine-readable results.
- **Large output truncation**: MCP tool responses may be truncated (e.g. `webReader` on long pages). For important results, pipe to a temp file first, then read targeted sections:
  ```bash
  mcporter call web-reader.webReader url="..." retain_images=false > /tmp/page.json
  # Then parse with jq/python, read specific sections, etc.
  ```
  This avoids re-fetching if parsing is truncated.