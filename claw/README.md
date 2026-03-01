# aiclaw

将 ai 项目的 agent 内核嵌入 picoclaw，实现"换心脏"。

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
│  │  cmd/aiclaw                                              ││
│  │  - 加载配置                                              ││
│  │  - 创建 channels + bus                                   ││
│  │  - 运行 AgentLoop                                        ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
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
