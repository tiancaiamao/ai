# ai - RPC-First Agent Core (Go)

`ai` 是一个 Go 实现的核心 Agent Loop，面向 `stdin/stdout` RPC 使用方式，适合编辑器集成。

## 构建与运行

```bash
cd /Users/genius/project/ai
go build -o bin/ai ./cmd/ai
```

```bash
# 默认启动 win 模式,给 ad 编辑器使用(github.com/tiancaiamao/ad)
./bin/ai
```

```bash
# 启动 RPC 模式
export ZAI_API_KEY=your-zai-api-key
./bin/ai --mode rpc
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
echo '{"type":"prompt","message":"Hello!"}' | ./bin/ai --mode rpc
echo '{"type":"get_state"}' | ./bin/ai --mode rpc
echo '{"type":"get_messages"}' | ./bin/ai --mode rpc
```

## 已实现的 RPC 命令

- 基础交互：`prompt`, `steer`, `follow_up`, `abort`
- 会话：`new_session`, `switch_session`, `delete_session`, `list_sessions`, `clear_session`
- 状态与统计：`get_state`, `get_messages`, `get_session_stats`, `get_last_assistant_text`
- 模型：`get_available_models`, `set_model`, `cycle_model`
- 压缩与思考级别：`compact`, `set_auto_compaction`, `set_tool_call_cutoff`, `set_tool_summary_strategy`, `set_thinking_level`, `cycle_thinking_level`
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
- 会话：`~/.ai/sessions/--<cwd>--/*.jsonl`（按工作目录隔离）
- 全局技能：`~/.ai/skills/`
- 项目技能：`.ai/skills/`

## 工具

内置工具：`read`, `bash`, `write`, `grep`, `edit`

## 新架构: Context Snapshot Architecture

本项目实现了新的 **Context Snapshot Architecture**，引入了基于事件溯源的上下文管理系统。

### 核心特性

1. **Event Log + Snapshot 模式**
   - 消息以不可变日志形式存储在 `messages.jsonl`
   - 活跃状态重建自 checkpoint + journal
   - 支持状态回滚和历史追溯

2. **双模式运行**
   - **Normal 模式**: 任务执行
   - **Context Management 模式**: 上下文重塑

3. **系统监控，LLM 决策**
   - 系统监控触发条件（token 使用、stale 输出等）
   - LLM 决定如何管理上下文（更新、截断、跳过）

4. **结构化上下文分离**
   - LLMContext: LLM 维护的结构化上下文
   - RecentMessages: 最近对话历史
   - AgentState: 系统维护的元数据

### 触发条件

- **Token 阈值**: 40% 正常触发，75% 紧急模式
- **Stale 输出**: 15+ 个陈旧工具输出
- **定期检查**: 每 10 个 turn 检查一次
- **最小间隔**: 正常触发之间至少间隔 3 个 turn

### 存储结构

```
~/.ai/sessions/--<cwd>--/
├── checkpoints/
│   ├── checkpoint_00010/
│   │   ├── llm_context.txt
│   │   └── agent_state.json
│   ├── checkpoint_00020/
│   └── current -> checkpoint_00020/
├── messages.jsonl
└── checkpoint_index.json
```

### 实施状态

- ✅ Phase 1-9: 核心功能已实现
- ⏸ Phase 10: 新旧架构并存（旧代码未删除）
- 📝 文档: 见 `design/IMPLEMENTATION_SUMMARY.md`

详见设计文档:
- `design/context_snapshot_architecture.md` - 架构设计
- `design/context_management_redesign_rationale.md` - 重写理由
- `design/IMPLEMENTATION_SUMMARY.md` - 实施总结

## 备注

- 不带参数默认启动 win 模式；RPC 模式用 `--mode rpc`。
- `ai` 采用 OpenAI 兼容的 Chat Completions 协议对接 ZAI。

## 许可证

与原项目保持一致（参考 [pi-mono](https://github.com/badlogic/pi-mono) 的核心实现）。
