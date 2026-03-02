# AiClaw 文档

AiClaw 是一个基于 AI 的聊天机器人，支持多通道（飞书等）和定时任务。

## 目录

- [快速开始](#快速开始)
- [配置](#配置)
- [命令](#命令)
- [Cron 定时任务](#cron-定时任务)
- [技能系统](#技能系统)
- [开发](#开发)

## 快速开始

```bash
# 构建
cd claw && go build -o bin/aiclaw ./cmd/aiclaw

# 启动 gateway（连接飞书等通道）
./bin/aiclaw

# 管理 cron 任务
./bin/aiclaw cron list
./bin/aiclaw cron add -n "每日提醒" -m "检查待办" -c "0 9 * * *"
```

## 配置

配置文件位于 `~/.aiclaw/` 目录：

```
~/.aiclaw/
├── config.json      # 主配置（模型、通道）
├── auth.json        # API 密钥
├── AGENTS.md        # 自定义身份提示词
├── cron/            # 定时任务
│   └── jobs.json
├── sessions/        # 会话存储
├── skills/          # 技能目录（软链接）
└── memory/          # tiered-memory 存储
```

### config.json

```json
{
  "model": {
    "id": "claude-3-5-sonnet-20241022",
    "provider": "anthropic",
    "baseUrl": ""
  },
  "voice": {
    "enabled": true,
    "provider": "zhipu"
  },
  "channels": {
    "feishu": {
      "app_id": "cli_xxx",
      "app_secret": "xxx"
    }
  }
}
```

### auth.json

```json
{
  "anthropic": { "apiKey": "sk-xxx" },
  "zhipu": { "apiKey": "xxx" },
  "zai": { "apiKey": "xxx" }
}
```

## 命令

```bash
# 启动 gateway（连接通道，处理消息）
./bin/aiclaw

# 启动时启用 trace 调试
./bin/aiclaw -trace

# 设置日志级别
./bin/aiclaw -log-level debug

# Cron 任务管理
./bin/aiclaw cron list
./bin/aiclaw cron add -n "名称" -m "消息" -c "0 9 * * *"
./bin/aiclaw cron remove <id>
./bin/aiclaw cron enable <id>
./bin/aiclaw cron disable <id>
```

## Cron 定时任务

AiClaw 支持 cron 表达式和固定间隔两种调度方式。

### 添加任务

```bash
# 每天 9:00 执行
./bin/aiclaw cron add -n "早间提醒" -m "开始新的一天！" -c "0 9 * * *"

# 每 60 秒执行一次
./bin/aiclaw cron add -n "心跳" -m "ping" -e 60

# 每天 18:00 生成日报
./bin/aiclaw cron add -n "日报" -m "请生成今日工作总结" -c "0 18 * * *"
```

### Cron 表达式格式

```
┌───────────── 分钟 (0 - 59)
│ ┌───────────── 小时 (0 - 23)
│ │ ┌───────────── 日 (1 - 31)
│ │ │ ┌───────────── 月 (1 - 12)
│ │ │ │ ┌───────────── 星期 (0 - 6，0=周日)
│ │ │ │ │
* * * * *
```

常用示例：
- `0 9 * * *` - 每天 9:00
- `30 18 * * 1-5` - 周一到周五 18:30
- `0 */2 * * *` - 每 2 小时
- `0 0 1 * *` - 每月 1 日 0:00

### 管理任务

```bash
# 列出所有任务
./bin/aiclaw cron list

# 输出示例：
# Scheduled Jobs:
# ---------------
#   [b28a1f52] 早间提醒
#       Schedule: 0 9 * * *
#       Message:  开始新的一天！
#       Status:   ✓ enabled
#       Next run: 2026-03-03 09:00

# 禁用任务
./bin/aiclaw cron disable b28a1f52

# 启用任务
./bin/aiclaw cron enable b28a1f52

# 删除任务
./bin/aiclaw cron remove b28a1f52
```

### 工作原理

1. 任务存储在 `~/.aiclaw/cron/jobs.json`
2. Gateway 启动时自动加载并启动调度器
3. 每秒检查是否有任务到期
4. 到期时发送消息给 agent 处理

## 技能系统

技能目录位于 `~/.aiclaw/skills/`（软链接到 `claw/skills/`）。

### 内置技能

- **tiered-memory** - 三层记忆系统（hot/warm/cold）
- **agent-self-governance** - 自主治理
- **intelligent-router** - 智能路由
- 更多技能见 `skills/` 目录

### tiered-memory 用法

```bash
cd ~/.aiclaw/skills/tiered-memory

# 存储记忆
python3 scripts/memory_cli.py store \
  --text "用户喜欢简洁的回复" \
  --category "preferences" \
  --importance 0.8

# 检索记忆
python3 scripts/memory_cli.py retrieve \
  --query "用户偏好" \
  --llm \
  --limit 5

# 查看统计
python3 scripts/memory_cli.py metrics
```

## 开发

### 项目结构

```
claw/
├── cmd/aiclaw/         # 主程序入口
│   ├── main.go         # 主逻辑
│   └── cmd_cron.go     # cron CLI
├── pkg/
│   ├── adapter/        # AgentLoop 适配器
│   ├── cron/           # Cron 服务
│   └── voice/          # 语音转录
├── skills/             # 技能目录（27个）
├── docs/               # 文档
├── go.mod
└── README.md
```

### 依赖

- `github.com/sipeed/picoclaw` - 通道管理
- `github.com/adhocore/gronx` - Cron 表达式解析
- `github.com/tiancaiamao/ai` - AI Agent 核心

### 构建

```bash
cd claw
go build -o bin/aiclaw ./cmd/aiclaw
```

## 相关链接

- [AiClaw 主项目](../) - AI Agent 核心
- [PicoClaw](https://github.com/sipeed/picoclaw) - 通道管理