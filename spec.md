# Feature: Subagent Orchestration

## Overview

为 ~/project/ai 实现 subagent 编排能力，使主 agent 能够自动分析任务复杂度，将复杂任务分解为子任务，选择合适的 agent persona 执行，最后汇总结果返回给用户。

## User Stories

### P1 (Must Have)
- 作为用户，我可以通过 `/orchestrate` 技能调用 subagent 编排功能
- 作为用户，我只需要给出任务描述，系统自动决定是否需要分解任务
- 作为用户，我只接收最终结果，不感知内部实现细节

### P2 (Should Have)
- 作为开发者，我可以通过命令行指定 `--system-prompt-file <path>` 来加载自定义 system prompt
- 作为开发者，我可以在 `~/.ai/skills/orchestrate/agents/` 目录下定义多个 agent persona 文件
- 作为用户，orchestrate 能够自动选择合适的 agent persona 执行子任务

### P3 (Nice to Have)
- 作为开发者，我可以为不同 agent persona 指定不同的可用工具限制
- 作为用户，我可以指定子任务是否并行执行

## Acceptance Criteria

- [ ] ai CLI 支持 `--system-prompt-file` 参数，读取文件内容作为 system prompt
- [ ] ai CLI 支持 `--system-prompt` 参数，直接指定字符串作为 system prompt
- [ ] `~/.ai/skills/orchestrate/agents/` 目录下存在默认的 agent persona 文件
- [ ] orchestrate skill 能够分析任务复杂度，判断是否需要分解
- [ ] orchestrate skill 能够自动选择合适的 agent persona
- [ ] orchestrate skill 能够 spawn subagent 并传递正确的 system-prompt-file
- [ ] orchestrate skill 能够汇总所有 subagent 结果返回给用户
- [ ] 与现有 workflow (wf-*) 正交，不依赖任务队列

## Success Criteria

- 用户执行 `/orchestrate 帮我实现一个 lisp 解释器` 能够自动完成复杂任务分解和执行
- 单元测试覆盖 CLI 参数解析
- 手动测试验证 orchestrate 完整流程