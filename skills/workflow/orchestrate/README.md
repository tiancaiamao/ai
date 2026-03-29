# orchestrate: Code-Based Orchestration CLI

独立的多 agent 协作 CLI，基于代码的状态机（非 prompt 驱动）。

## 特性

- **代码驱动** — 状态机在 Go 代码中，不依赖 LLM 生成控制流
- **tmux 集成** — Worker 在独立 tmux 窗口中运行
- **超时处理** — 任务超时自动失败并重试
- **心跳检测** — 监测 Worker 健康状态
- **日志聚合** — 统一查看所有任务日志
- **Human-in-the-loop** — 支持 checkpoint 审批机制

## 安装

```bash
cd skills/workflow/orchestrate
go install ./cmd
```

## 快速开始

```bash
# 在项目目录启动
cd /your/project
orchestrate start --workflow feature

# 另一终端监控
orchestrate status
orchestrate logs
```

## Human-in-the-loop

### Workflow 配置

在 workflow YAML 中配置审批点：

```yaml
name: feature
description: Feature development workflow with human checkpoints

human_loop:
  # 这些阶段需要人工审批后才能继续
  review_phases:
    - plan    # 计划阶段完成后暂停，等待审批
  
  # 审批超时自动通过（0 = 永不，必须手动审批）
  auto_approve_timeout: 0

phases:
  - id: explore
    subject: "Explore codebase"
    description: |
      探索代码库...
  
  - id: plan
    subject: "Create implementation plan"
    blocked_by: [explore]
    description: |
      创建实现计划...
      
      *** CHECKPOINT ***
      完成后请求审批:
      orchestrate api request-review --input '{
        "task_id": "plan",
        "phase_id": "plan",
        "worker_name": "$AI_TEAM_WORKER",
        "summary": "计划创建完成",
        "output_file": ".ai/team/outbox/plan.md"
      }'
```

### 审批流程

1. **Worker 请求审批** — 完成任务后调用 `request-review`
2. **Runtime 暂停** — 等待人工审批
3. **人工审批** — 用户运行 `orchestrate review approve/reject`
4. **继续执行** — 审批通过后继续下一阶段

```bash
# 查看待审批任务
orchestrate review

# 查看详情
orchestrate review show plan

# 审批通过
orchestrate review approve plan --comment "计划可行，继续执行"

# 拒绝
orchestrate review reject plan --comment "需要更多细节"
```

### Worker 等待审批

Worker 在请求审批后，需要轮询等待结果：

```bash
# 请求审批
orchestrate api request-review --input '{"task_id":"plan",...}'

# 轮询等待
while true; do
  result=$(orchestrate api check-review --input '{"task_id":"plan"}')
  status=$(echo "$result" | jq -r '.status')
  
  if [ "$status" = "completed" ]; then
    approved=$(echo "$result" | jq -r '.approved')
    if [ "$approved" = "true" ]; then
      echo "Approved! Continuing..."
      break
    else
      echo "Rejected: $(echo "$result" | jq -r '.comment')"
      # 修改计划，重新请求审批
    fi
  fi
  
  sleep 10
done
```

## CLI 命令

### 启动

```bash
orchestrate start --workflow feature \
  --workers 5 \           # 并发 worker 数量
  --timeout 1h \          # 任务超时
  --heartbeat-ttl 10m \   # 心跳 TTL
  --name my-team          # 团队名称
```

### 监控

```bash
orchestrate status              # 状态
orchestrate logs [task]         # 日志
orchestrate logs task --follow  # 实时日志
orchestrate capture [worker]    # 捕获 tmux 输出
```

### 交互

```bash
orchestrate attach              # 附加到 tmux
orchestrate attach worker-plan  # 附加到特定 worker
orchestrate stop                # 停止团队
```

### 审批

```bash
orchestrate review              # 待审批列表
orchestrate review show <task>  # 查看详情
orchestrate review approve <task> --comment "..."
orchestrate review reject <task> --comment "..."
```

### Worker API

```bash
# 任务生命周期
orchestrate api create-task --input '{"subject":"...","description":"..."}'
orchestrate api claim-task --input '{"task_id":"1","worker":"w1"}'
orchestrate api complete-task --input '{"task_id":"1","claim_token":"...","summary":"Done"}'
orchestrate api fail-task --input '{"task_id":"1","claim_token":"...","error":"Failed"}'

# 审批
orchestrate api request-review --input '{"task_id":"plan",...}'
orchestrate api check-review --input '{"task_id":"plan"}'
```

## 与主 ai agent 集成

### 方式 1: 作为独立进程

orchestrate 是独立的 CLI，在项目目录运行：

```bash
# 终端 1: 启动团队
cd /your/project
orchestrate start --workflow feature

# 终端 2: 主 ai agent
ai
> 帮我实现用户登录功能

# 终端 3: 监控和审批
orchestrate status
orchestrate review
```

### 方式 2: 在 ai agent 中调用

在 ai agent 中使用 shell 命令：

```
> 启动 orchestrate 来处理这个功能
! orchestrate start --workflow feature --cwd /project

> 查看状态
! orchestrate status --cwd /project

> 审批计划
! orchestrate review approve plan --comment "继续"
```

### 方式 3: 作为 Tool（需要开发）

可以将 orchestrate 封装为 ai agent 的 tool：

```go
// 在 pkg/tools/team.go 中
func NewTeamTool() *Tool {
    return &Tool{
        Name: "ai_team",
        Description: "Manage multi-agent team workflows",
        Execute: func(input map[string]interface{}) (string, error) {
            cmd := input["command"].(string)
            // 执行 orchestrate 命令
            out, err := exec.Command("orchestrate", strings.Fields(cmd)...).Output()
            return string(out), err
        },
    }
}
```

## 架构

```
.ai/team/
├── config.json       # 团队配置
├── state.json        # 运行时状态
├── tasks/            # 任务文件
├── workers/          # Worker 状态
├── logs/             # 任务日志
├── reviews/          # 审批请求
│   ├── plan.json     # 待审批
│   └── results/      # 审批结果
└── outbox/           # 任务输出
```

## 工作流程示例

```
1. 启动团队
   orchestrate start --workflow feature

2. explore 阶段自动开始
   Worker: worker-explore
   Status: in_progress

3. explore 完成，自动触发 plan
   Worker: worker-plan
   Status: in_progress

4. plan 完成，请求审批
   Worker 调用: orchestrate api request-review
   Runtime: 暂停，等待审批

5. 人工审批
   $ orchestrate review
   $ orchestrate review show plan
   $ orchestrate review approve plan --comment "OK"

6. 审批通过，继续执行
   plan → completed
   下一阶段任务开始

7. 所有任务完成
   Runtime: 自动停止
```

## 文件结构

```
skills/workflow/orchestrate/
├── go.mod
├── go.sum
├── README.md
├── api.go              # Task API + Review API
├── storage.go          # 文件操作 + 锁
├── runtime.go          # 依赖图执行 + 审批检测
├── tmux.go             # tmux 集成
├── types.go            # 类型定义
├── worker.go           # Worker 管理
├── cmd/
│   └── main.go         # CLI 入口
└── templates/
    ├── feature.yaml    # 功能开发（含审批）
    └── bugfix.yaml     # Bug 修复
```
