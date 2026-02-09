# ai - RPC-First Agent Core (Go)

`ai` 是一个 Go 实现的核心 Agent Loop，面向 `stdin/stdout` RPC 使用方式，适合编辑器集成。

## 定位说明

- 本仓库只提供 **RPC 模式** 的 Agent 核心，不包含 TUI 模块。
- 在 ad 编辑器中使用的方式是 `win-ai`（由 ad 的 win 库驱动），位于 `github.com/tiancaiamao/ad` 的集成路径中。

## 构建与运行

```bash
cd /Users/genius/project/ai
go build -o bin/ai ./cmd/ai
```

```bash
# 默认使用 ZAI
export ZAI_API_KEY=your-zai-api-key
./bin/ai --mode rpc
```

构建 win-ai（供 ad 编辑器使用）：

```bash
go build -o bin/win-ai ./cmd/win-ai
```

加载指定会话文件：

```bash
./bin/ai --mode rpc --session /abs/path/to/session.jsonl
```

## 环境变量

- `ZAI_API_KEY`（必需）
- `ZAI_BASE_URL`（可选，默认 `https://api.z.ai/api/coding/paas/v4`）
- `ZAI_MODEL`（可选，默认 `glm-4.5-air`）

也可以在 `~/.ai/auth.json` 写入 API Key（优先级：环境变量 > auth.json）：

```json
{
  "zai": {
    "type": "api_key",
    "key": "your-zai-api-key"
  }
}
```

## RPC 使用示例

```bash
echo '{"type":"prompt","message":"Hello!"}' | ./bin/ai
echo '{"type":"get_state"}' | ./bin/ai
echo '{"type":"get_messages"}' | ./bin/ai
```

## 已实现的 RPC 命令

- 基础交互：`prompt`, `steer`, `follow_up`, `abort`
- 会话：`new_session`, `switch_session`, `delete_session`, `list_sessions`, `clear_session`
- 状态与统计：`get_state`, `get_messages`, `get_session_stats`, `get_last_assistant_text`
- 模型：`get_available_models`, `set_model`
- 压缩与思考级别：`compact`, `set_auto_compaction`, `set_thinking_level`, `cycle_thinking_level`
- 分叉：`get_fork_messages`, `fork`
- 命令列表：`get_commands`（当前返回已加载的 skills）

## 事件

- `server_start`
- `agent_start`, `agent_end`
- `turn_start`, `turn_end`
- `message_start`, `message_update`, `message_end`
- `tool_execution_start`, `tool_execution_end`

`message_update` 的 `assistantMessageEvent.type`：
- `text_start`, `text_delta`, `text_end`
- `toolcall_delta`

## 文件位置

- 配置：`~/.ai/config.json`
- 日志：`~/.ai/ai.log`
- 会话：`~/.ai/*.jsonl`
- 全局技能：`~/.ai/skills/`
- 项目技能：`.ai/skills/`

## 工具

内置工具：`read`, `bash`, `write`, `grep`, `edit`

## 备注

- 仅支持 `--mode rpc`。
- `ai` 采用 OpenAI 兼容的 Chat Completions 协议对接 ZAI。

## 许可证

与原项目保持一致（基于 [pi-mono](https://github.com/badlogic/pi-mono) 的核心实现）。
