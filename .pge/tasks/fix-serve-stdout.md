# Task: 移除 ai serve 的 stdout 输出

## 问题描述
`ai serve` 子命令在启动时会把 run ID 输出到 stdout（`fmt.Println(id)`）。
这导致 `ai serve &` 后台运行时，stdout 输出会干扰 shell 行为。

## 修复要求
- 移除 `cmd/ai/run.go` 中 `serveSubcommand` 函数里的 `fmt.Println(id)` 及其注释
- **不要**影响 `--id-file` 机制（那是获取 run ID 的正确方式）
- **不要**修改其他函数（如 `runSubcommand` 等）
- 确保 `go build` 能通过

## 文件
- `cmd/ai/run.go` — 搜索 `// Print run ID to stdout`，删除该注释和下一行 `fmt.Println(id)`