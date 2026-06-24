---
name: code-rag
description: |
  为任意代码仓库构建结构化知识库（code wiki），并通过 frozen snapshot 机制启动即问即答的 expert agent。
  当你需要深入理解一个大型代码仓库、构建可复用的代码知识库、或为其他 agent 提供代码专家服务时使用此技能。
metadata:
  goclaw:
    emoji: "🏗️"
    requires:
      bins: ["ai"]
---

# Code RAG — 构建代码仓库的结构化知识库

将大型代码仓库的源码知识**编译**成结构化的 wiki 页面，再通过 frozen snapshot 机制把 wiki 全量加载到 expert agent 上下文中，实现零延迟 RAG。

核心思想：**编译一次，反复使用。** 不是每次从源码重新检索，而是预先系统性提炼知识，之后即问即答。

## When to Use

- 面对一个不熟悉的大型代码仓库，需要系统性理解其架构和设计
- 需要构建一个可长期维护、可反复查询的代码知识库
- 需要为其他 agent 提供某个项目的代码专家服务（作为 subagent）
- 需要理解跨模块的调用链、设计决策、隐性约束
- 项目有设计文档需要和源码知识整合

**不要用于**：
- 小项目（< 50 个源文件）—— 直接读源码更高效
- 纯使用问题（不是源码层面的问题）
- 一次性问题（不值得构建整个 wiki）

## 已建项目

以下项目已构建好 wiki，可直接启动 expert agent 使用：

| 项目 | Wiki 路径 | 源码路径 | 子系统数 | 大小 |
|------|----------|---------|---------|------|
| OceanBase | `~/project/oceanbase-code-RAG/` | `~/project/oceanbase/` | 9 | 321KB |
| TiDB | `~/project/tidb-code-RAG/` | `~/project/tidb/` | 10 | 558KB |
| CSE | `~/project/cloud-storage-engine/wiki` | `~/project/cloud-storage-engine/` | 11 子系统 + 6 流程 + 13 特性 | ~33 页面 |

**使用方式**：进入 wiki 目录，执行 `wiki/snapshot/snapshot.sh start`，然后用 `ask` 提问。
详见下方 Phase 3 章节。

## Architecture

```
~/project/<project>-code-RAG/       ← 项目 wiki 仓库
├── README.md                        ← 构建记录和维护指南
├── wiki/
│   ├── index.md                     ← 总索引（所有页面清单 + 分类）
│   ├── architecture.md              ← 顶层架构（组件图 + 跨模块数据流）
│   ├── subsystems/                  ← 子系统页面（主线）
│   │   ├── <subsystem-1>.md
│   │   └── <subsystem-2>.md
│   ├── features/                    ← 特性页面（辅线，可选）
│   │   ├── <feature-1>.md
│   │   └── <feature-2>.md
│   └── snapshot/
│       ├── frozen/                  ← 🔒 冻结镜像（只读）
│       ├── runtime/                 ← 🔧 工作副本（可脏）
│       ├── bootstrap.md             ← Agent 加载指令
│       └── snapshot.sh              ← 管理脚本
```

## 完整工作流

### Phase 1: Init — 初始化

```
1. 确定目标项目
   - 项目名、源码路径、主要语言
   - 是否有设计文档（docs/design/ 等）
   - 是否有官方技术文档

2. 创建 wiki 目录
   mkdir -p ~/project/<project>-code-RAG/wiki/{subsystems,features,snapshot}

3. 生成 snapshot.sh（从模板，填入项目名）
   cp <skill-dir>/templates/snapshot.sh ~/project/<project>-code-RAG/wiki/snapshot/
   # 编辑 PROJECT_NAME 和 EXPERT_LABEL

4. 生成 bootstrap.md（从模板，Phase 2 结束后再填入子系统列表）
   cp <skill-dir>/templates/bootstrap.md ~/project/<project>-code-RAG/wiki/snapshot/
```

### Phase 2: Build — 构建 wiki 内容

构建是分轮次进行的，每轮用 subagent 并行探索。

#### Round 0: 架构探索 + 子系统划分

agent 扫描项目结构后自主决定子系统划分。

```
输入：README、入口文件（main/lib.rs/go.mod）、目录结构、Makefile/Cargo.toml
输出：
  - wiki/architecture.md  — 顶层架构图、组件关系、核心数据流
  - wiki/index.md         — 子系统清单 + features 清单（占位）
  - 子系统划分方案（供 Round 1 使用）
```

子系统识别策略（agent 自主判断）：
1. 优先按项目的 `components/` 或 `pkg/` 目录划分
2. 如果项目是单模块，按职责域划分（如 parser/planner/executor）
3. 合并太小或关系紧密的模块（< 5 个核心文件的模块应合并到相邻模块）
4. 目标粒度：每个子系统 15-40KB wiki 页面
5. 子系统数量：通常 5-15 个
6. agent 自行决定哪些文件值得读、哪些跳过（测试、生成代码、vendor 等）

#### Round 1: 子系统探索（并行 subagent）

每个 subsystem 分配一个 subagent，并行探索。

```
subagent 数量 = min(子系统数量, 5-6)  # 每个 subagent 负责 1-3 个子系统
每个 subagent 任务：
  1. 读源码核心文件（每个子系统 ≥ 10 个文件）
  2. 读已有的 agents/design 文档（如果有）
  3. 输出标准格式的 wiki 页面
```

**Subagent Prompt 模板**（见下方 Templates 章节）

#### Round 2: 设计文档整合（可选）

如果项目有设计文档（`docs/design/`、RFC 等），用 subagent 按主题分组阅读：

```
1. 列出所有设计文档，按子系统分组
2. 每个 subagent 负责一组设计文档
3. 为对应的 subsystem 页面补充「设计决策与取舍」段落
```

#### Round 3: 特性/官方文档整合（可选）

如果项目有官方用户文档，提炼出特性级别的知识：

```
1. 列出官方文档，按特性分组
2. 每个 subagent 负责一组
3. 输出 features/ 页面
```

#### Round 4: 交叉链接 + Lint

```
1. 检查所有 [[wikilinks]] 是否指向存在的页面
2. 补充跨子系统的「相关子系统」链接
3. 更新 index.md 为最终版
4. 确认每个页面都有 frontmatter（tags, date）
5. 检查 wiki 总大小（目标 < 200KB，超出则考虑精简或拆分）
```

### Phase 3: Serve — 启动 Expert Agent

```bash
cd ~/project/<project>-code-RAG

# 1. 构建冻结镜像（首次约 3-5 分钟）
wiki/snapshot/snapshot.sh freeze

# 2. Fork 并启动
wiki/snapshot/snapshot.sh start

# 3. 提问
wiki/snapshot/snapshot.sh ask "你的问题"

# 4. 停止（保留工作副本，可 resume）
wiki/snapshot/snapshot.sh stop

# 5. 工作副本用脏了就清理
wiki/snapshot/snapshot.sh clean
```

#### Expert Agent 的源码搜索能力（semble）

Expert agent 预装了 wiki 知识，但遇到 wiki 未覆盖的细节时，可以用 **semble** 搜索源码补充。

semble 是语义代码搜索引擎（~250ms 索引，~1.5ms 查询），在 bootstrap.md 中配置搜索指令。
Expert agent 自行判断何时需要搜索——调用方无需关心。

```
wiki 有答案 → 直接回答（秒级）
wiki 不够   → semble 搜索源码 → 补充回答（多几秒）
```

**前提**：semble 已通过 `mcporter config add semble --command semble --scope home` 配置为 MCP 服务（expert agent 通过 `mcporter call` 调用，索引缓存不重复构建）。

在 bootstrap.md 的「源码搜索工具」章节中配置：
- 按子系统子目录搜索（避免搜整个项目，太慢）
- 语义搜索 + 符号搜索 + find-related
- 子系统目录对应关系复用 wiki/index.md 中的映射

#### 作为 subagent 服务（推荐）

在其他 agent 中通过 `ai send` 调用：

```bash
# 1. 启动
cd ~/project/<project>-code-RAG
bash wiki/snapshot/snapshot.sh start

# 2. 获取 ID
EXPERT_ID=$(cat wiki/snapshot/expert.id)

# 3. 提问（可多次）
#    - 普通问题：2-3 分钟足够
#    - 深度问题（需要搜源码、跨子系统分析）：建议 10 分钟
ai send --id "$EXPERT_ID" --wait --timeout 10m "你的问题"

# 4. 用完停止
bash wiki/snapshot/snapshot.sh stop

# 5. 注册为 subagent（如需跟踪）
echo "$EXPERT_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**超时建议**：expert agent 遇到 wiki 未覆盖的问题时会用 semble 搜源码补充，
这需要额外时间。建议统一使用 `--timeout 10m`，避免深度问题被截断。

**长回答技巧**：如果预期回答会很长（如"全面梳理 XX 的全部资料"），
让 expert 写入文件而不是流式输出，避免截断：

```bash
# 方式一：让 expert 写文件（推荐用于长回答）
ai send --id "$EXPERT_ID" --wait --timeout 10m \
  "请将完整回答写入 /tmp/ob-answer.md，写完后输出'已写入'即可"

# 方式二：如果已经截断，发 follow-up 补救
ai send --id "$EXPERT_ID" --wait --timeout 10m \
  "请基于你刚才搜到的信息，将完整回答写入 /tmp/ob-answer.md"
```

### Phase 4: Maintain — 维护更新

```
1. 拉取最新源码
2. git diff / git log --stat 评估受影响的子系统
3. 用 subagent 重新探索受影响的子系统，更新 wiki
4. wiki/snapshot/snapshot.sh freeze   — 重建冻结镜像
5. wiki/snapshot/snapshot.sh clean && wiki/snapshot/snapshot.sh start
```

更新频率：

| 变更规模 | 操作 |
|---------|------|
| bug fix / 小重构 | 不需要更新 |
| 功能迭代 | 更新对应的 subsystems 页面 |
| 新特性 | 新建 features 页面，更新 index |
| 大版本升级 | 全面重新评估 |

## Templates

### Wiki 页面标准格式

每个 wiki 页面包含以下段落：

```markdown
---
tags: [subsystem, keyword1, keyword2]
date: YYYY-MM-DD
sources: [source-file-slug]
---

# <子系统/特性名>

> 一句话概述

## 概述
本节在全系统中的位置、核心职责、对外接口。

## 原理与数据结构
核心数据结构、算法、设计模式。包含关键 struct/class 的关系图。

## 关键流程
主要操作的生命周期/状态机/调用链。

## 源码导航
关键文件路径列表（相对项目根目录），每个文件一行注释说明职责。

## 配置与变量
相关的配置项、环境变量、命令行参数。

## 设计决策与取舍
为什么这样设计，考虑过哪些替代方案，各自 trade-off。
（从设计文档、PR review、commit message 中提炼）

## 使用场景与最佳实践
典型用法、推荐配置、性能考量。

## 隐性知识 / 踩坑指南
源码中不明显但重要的约束、常见陷阱、调试技巧。

## 相关子系统
- [[other-subsystem]] — 说明关联关系

## 参考
- [设计文档链接]
- [RFC 链接]
```

### Subagent Prompt 模板

```
你是 <PROJECT_NAME> 源码 wiki 的构建者。请探索以下子系统，输出结构化 wiki 页面。

## 探索范围
[列出需要阅读的源码目录]
[列出可参考的设计文档]

## 源码位置
<SOURCE_PATH>

## 输出要求
写入文件 <wiki/subsystems/xxx.md>，包含以下段落：
1. 概述  2. 原理与数据结构  3. 关键流程  4. 源码导航
5. 配置与变量  6. 设计决策与取舍(★)  7. 使用场景与最佳实践
8. 隐性知识/踩坑指南  9. 相关子系统 + 参考

## 注意事项
- 源码导航中的路径用相对项目根目录的路径
- 关键流程要画出调用链（A → B → C）
- 设计决策要说清楚「为什么」而不只是「是什么」
- 踩坑指南从代码注释、TODO、已知问题中提炼
- 自行判断哪些文件值得深入、哪些可以跳过（测试/生成代码/vendor 等）
```

## Frozen ↔ Runtime 设计

```
frozen/ (只读)              runtime/ (可变)
N 条消息, ~100-200KB         随提问增长
    │                           │
    │  start = cp               │  ask × N
    │ ──────────────►           │ ──────────► session 越来越大
    │                           │
    │  clean = rm               │
    │ ◄────────────────         │
    │                           │
    ▼                           ▼
  永远干净                    用脏了就扔
```

**关键原则：frozen 只读，runtime 随便脏。**

- **freeze**：启动临时 agent，让它读完所有 wiki 页面，保存 session 为冻结镜像
- **start**：从 frozen 复制到 runtime，启动 expert agent
- **ask**：向 agent 提问，消息追加到 runtime
- **clean**：丢弃 runtime，下次 start 重新 fork

## Commands Reference

| 命令 | 用途 |
|------|------|
| `snapshot.sh freeze` | 重建冻结镜像（wiki 更新后执行） |
| `snapshot.sh start` | Fork frozen → runtime，启动 expert |
| `snapshot.sh ask "Q"` | 提问（等回答） |
| `snapshot.sh stop` | 停止（保留 runtime，可 resume） |
| `snapshot.sh clean` | 清除 runtime（下次重新 fork） |
| `snapshot.sh reset` | 清除一切（frozen + runtime） |
| `snapshot.sh status` | 查看状态 |

## Troubleshooting

| 问题 | 解决 |
|------|------|
| `frozen/ 不存在` | 先执行 `snapshot.sh freeze` |
| agent 启动但无响应 | `tmux attach -t <project>-expert` 检查 |
| 回答质量下降 | `snapshot.sh clean && snapshot.sh start` |
| wiki 内容过时 | 更新 wiki 后 `snapshot.sh freeze` |
| `ai ls` 看不到 agent | `snapshot.sh stop && snapshot.sh start` |
| 构建冻结镜像超时 | 检查 wiki 总大小，可能需要拆分 bootstrap.md |
| wiki 超过 200KB | 合并小模块或精简内容 |

## References

- **TiDB Code RAG**（完整实例）：`~/project/tidb-code-RAG/`
  - 10 个子系统页面、15 个特性页面
  - 从 TiDB 源码 + 109 篇设计文档 + 106 篇官方文档中提炼约 558KB
  - 构建过程记录在 `README.md` 的「构造方法」章节
- **Wiki 技能**（通用 wiki 维护方法论）：`~/.ai/skills/wiki/SKILL.md`