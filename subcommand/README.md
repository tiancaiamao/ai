# Subcommands

CLI subcommand implementations (run, serve, ls, watch, send, kill).

## Structure

```
subcommand/
├── acp/        # ACP stdio entry point
├── helpers/    # Shared utilities (ParseSystemPrompt, ResolveRunID)
├── kill/       # kill subcommand
├── ls/         # ls subcommand
├── run/        # run, serve, and watch subcommands
└── send/       # send subcommand
```


## Notes

- **run/** combines `run`, `serve`, and `watch` subcommands in one package to share TUI models (runModel embeds watchModel)
- **tui/** contains pure TUI utilities shared across subcommands
- **helpers/** contains CLI utilities (system prompt parsing, run ID resolution)