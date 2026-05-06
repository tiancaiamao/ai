---
name: wf
description: 精简的门控工具，管理 brainstorm → plan → implement 流程中的 gate 状态。约束放在工具里，不放在提示词里。
---

# wf — Gate Control

精简的门控 CLI，管理 `design → plan → implement` 三阶段流程的 gate 状态。

## 安装

```bash
cd ~/.ai/skills/wf && go build -o bin/wf . && cp bin/wf ~/go/bin/wf
```

## Commands

```bash
wf init <description>              # 初始化 .wf/state.json（design → plan → implement）
wf status [--json]                 # 当前阶段、gate 状态
wf approve --message "用户原话"     # 确认当前 gate（必须带用户原话）
wf reject "feedback"               # 打回 gate
wf advance [--output file]         # 进入下一阶段（校验 gate + 产出文件）
wf note "progress text"            # 进度备注
wf back [steps]                    # 回退到之前的阶段
```

## 硬约束（工具层面，不可绕过）

- `wf advance` 在 gate 未 approve 时返回错误 + 退出码 1
- `wf approve` 不带 `--message` 返回错误
- `wf advance --output <file>` 文件不存在时返回错误
- `--message` 内容记录到 state.json，可审计是否为用户原话

## State File

`.wf/state.json`

```json
{
  "id": "wf-xxx",
  "description": "add feature X",
  "phases": [
    {"name": "design", "gate": true, "status": "completed", "output": "design.md", "approvedAt": "...", "approveMessage": "方案可以"},
    {"name": "plan", "gate": true, "status": "active"},
    {"name": "implement", "gate": false, "status": "pending"}
  ],
  "currentPhase": 1,
  "status": "in_progress"
}
```

## 设计原则

约束放在工具里，不要放在提示词里。
wf 工具的硬约束确保 agent 无法跳过 gate，无论 SKILL.md 怎么写。