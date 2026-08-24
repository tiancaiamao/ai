# Skills

This directory holds **first-party skills written by the repository owner**
only. They are tracked in git and shared with the `ai` CLI through a symlink
from `~/.ai/skills/<name>` back into this directory.

## Rules

- Only add a skill here if you wrote it yourself.
- Third-party or externally maintained skills (e.g. from `nutshell-skills`,
  `clinic-cli`, or other project repos) must **not** be placed or symlinked
  here. Install them directly under `~/.ai/skills/` instead — as real
  directories or as symlinks pointing to their origin repositories.
- Keep the link direction outward: this repo never references external
  skill sources; `~/.ai/skills/` references in.

## Layout

```
~/.ai/skills/            real directory (not a symlink to this repo)
├── <self-written>/  ->  /home/genius/project/ai/skills/<self-written>   (symlink)
└── <external>/      ->  /path/to/origin/repo/skill                      (symlink)
```

This keeps `git status` in the `ai` repository clean while making all skills
visible to the CLI.