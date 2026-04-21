# AiClaw 🦞

将 ai 项目的 agent 内核嵌入 picoclaw，实现"换心脏"。

## 文档

- [完整文档](docs/README.md) - 配置、命令、技能系统
- [Cron 定时任务](docs/cron.md) - 定时任务使用指南

## 快速开始

```bash
# 构建
cd claw && go build -o bin/aiclaw ./cmd/aiclaw

# 启动 gateway
./bin/aiclaw

# 管理 cron 任务
./bin/aiclaw cron list
./bin/aiclaw cron add -n "每日提醒" -m "检查待办" -c "0 9 * * *"
```

## 功能

- **多通道支持**: 飞书、Telegram、Discord 等
- **语音转录**: 支持智谱 ASR 和 Groq Whisper
- **定时任务**: Cron 表达式和固定间隔调度
- **技能系统**: 27+ 内置技能（tiered-memory 等）
- **会话隔离**: 按 Channel:ChatID 隔离
- **Web 前端**: 可对接 PicoClaw 的 Web UI 进行聊天和管理

## Web 前端对接

aiclaw 内置了 Web 服务器（`pkg/web/server.go`），实现了与 PicoClaw Web 前端兼容的 API，
可以直接对接 [PicoClaw](https://github.com/sipeed/picoclaw) 的 Web 前端界面。

### 前置条件

需要 PicoClaw 项目在本地可用（例如 `~/project/picoclaw/`）。

### 启动方式

**1. 启动 aiclaw 后端**

确保配置中启用了 Pico Channel（`channels.pico.enabled: true`），aiclaw 会自动在 `localhost:18800` 启动 Web 服务器：

```bash
cd claw
go build -o bin/aiclaw ./cmd/aiclaw
./bin/aiclaw
```

启动后会看到：
```
╔═══════════════════════════════════════════════════════════════╗
║  🌐 Claw Web Server                                         ║
╠═══════════════════════════════════════════════════════════════╣
║  Web UI: http://localhost:18800                              ║
╚═══════════════════════════════════════════════════════════════╝
```

**2. 启动 PicoClaw 前端 dev server**

PicoClaw 前端的 Vite 配置已默认将 API 请求代理到 `localhost:18800`，无需额外配置：

```bash
cd ~/project/picoclaw/web/frontend
pnpm install   # 首次需要安装依赖
pnpm dev
```

然后在浏览器打开 `http://localhost:5173` 即可使用 PicoClaw 的 Web UI 与 aiclaw 交互。

### 配置示例

在 `~/.aiclaw/config.json` 中启用 Web 服务：

```json
{
  "model": {
    "id": "your-model-id",
    "provider": "your-provider",
    "baseUrl": ""
  },
  "channels": {
    "pico": {
      "enabled": true
    }
  }
}
```

### 兼容的 API

aiclaw 的 Web 服务器实现了以下与 PicoClaw 前端兼容的 API：

| 路径 | 说明 |
|------|------|
| `/pico/ws` | WebSocket 聊天接口（支持 `message.send` / `ping` 协议） |
| `GET/POST /api/pico/token` | 获取/重新生成 Pico Channel 认证 token |
| `GET /api/sessions` | 列出会话 |
| `GET /api/sessions/{id}` | 获取会话详情 |
| `DELETE /api/sessions/{id}` | 删除会话 |
| `GET /api/status` | 服务器状态 |
| `GET/POST/PUT/DELETE /api/models` | 模型管理 |
| `GET/PUT/PATCH /api/config` | 配置读写 |
| `GET/PUT /api/channels` | 通道配置 |
| `GET /api/gateway/status` | 网关状态（始终返回 `running`） |

> **注意**：PicoClaw 前端的部分功能（如 skills、tools、oauth 管理）尚未在 aiclaw 中实现，
> 访问这些 API 会返回 404。聊天和模型/配置管理等核心功能可正常使用。

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│  picoclaw (引用，不修改)                                     │
│  ┌─────────────────────────────────────────────────────────┐│
│  │  pkg/channels  - Telegram, Discord, Slack, 飞书, ...   ││
│  │  pkg/bus       - MessageBus, InboundMessage, Outbound  ││
│  │  pkg/config    - 配置文件解析                           ││
│  │  pkg/media     - 媒体存储                               ││
│  └────────────────────────┬────────────────────────────────┘│
└───────────────────────────┼─────────────────────────────────┘
                            │
                            │ bus.InboundMessage / bus.OutboundMessage
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│  claw (本目录)                                               │
│  ┌─────────────────────────────────────────────────────────┐│
│  │  pkg/adapter                                             ││
│  │  - AgentLoop: 从 bus 消费消息                            ││
│  │  - Session: 按 Channel:ChatID 隔离会话                   ││
│  │  - 调用 ai 的 agent 内核处理                             ││
│  └─────────────────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────────────────┐│
│  │  pkg/web (Web Server)                                    ││
│  │  - 兼容 PicoClaw 前端的 REST API + WebSocket            ││
│  │  - 默认监听 localhost:18800                              ││
│  │  - 可独立对接 PicoClaw Web 前端                          ││
│  └─────────────────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────────────────┐│
│  │  cmd/aiclaw                                              ││
│  │  - 加载配置                                              ││
│  │  - 创建 channels + bus                                   ││
│  │  - 运行 AgentLoop + Web Server                           ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘

                    ┌──────────────────────┐
                    │  PicoClaw Web 前端    │
                    │  (Vite + React)       │
                    │  localhost:5173       │
                    └──────────┬───────────┘
                               │ API proxy → localhost:18800
                               ▼
                    ┌──────────────────────┐
                    │  aiclaw Web Server    │
                    │  localhost:18800      │
                    └──────────────────────┘
```

## 依赖隔离

本目录是独立的 Go 模块，通过 `replace` 指令引用父目录的 ai 项目：

```go
// go.mod
module github.com/tiancaiamao/ai/claw

require (
    github.com/sipeed/picoclaw v0.2.0
    github.com/tiancaiamao/ai v0.0.0
)

replace github.com/tiancaiamao/ai => ../
```

这样 ai 主项目的依赖不会被 picoclaw 污染。

## 使用方法

### 1. 创建配置文件

使用 picoclaw 的配置格式，保存为 `~/.aiclaw/config.json`:

```json
{
  "agents": {
    "defaults": {
      "provider": "zai",
      "model": "your-model-id"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "${TELEGRAM_BOT_TOKEN}"
    }
  },
  "model_list": [
    {
      "model_name": "default",
      "model": "zai/your-model-id",
      "api_key": "${ZAI_API_KEY}",
      "api_base": "https://api.zai.ai/v1"
    }
  ]
}
```

### 2. 设置环境变量

```bash
export ZAI_API_KEY=xxx
export TELEGRAM_BOT_TOKEN=xxx
```

### 3. 编译运行

```bash
cd claw
go build -o bin/aiclaw ./cmd/aiclaw
./bin/aiclaw
```

## 消息流转

```
用户消息 → Channel → bus.InboundMessage → AgentLoop → ai agent → 响应
                                                              ↓
                         Channel ← bus.OutboundMessage ←─────┘
```

1. Channel (如 TelegramChannel) 接收用户消息
2. 转换为 `bus.InboundMessage` 发布到 bus
3. `AgentLoop` 从 bus 消费消息
4. 按 `Channel:ChatID` 隔离会话
5. 调用 ai 的 agent 内核处理
6. 生成 `bus.OutboundMessage` 发布到 bus
7. Channel 发送响应给用户

## 维护说明

### picoclaw 更新时

只需关注 `bus.InboundMessage` 和 `bus.OutboundMessage` 的格式是否变化。

### ai 项目更新时

adapter 直接引用 ai 项目的包，自动获得更新。
