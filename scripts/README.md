# scripts/ — 运维脚本

本目录存放个人维护用的运维脚本。所有脚本幂等，可安全重复运行。

| 脚本 | 用途 |
|------|------|
| `install-skills.sh` | ai 技能安装/同步（详见下方） |
| `clean-sessions.sh` | 清理 `.ai/sessions` 下的空/短 session（用法见 `README-clean-sessions.md`） |
| `evolve_loop.sh` | 自动进化循环 |
| `planner_rpc_filter.py` | planner RPC 过滤 |
| `test.sh` / `test-common.sh` | 测试辅助 |

---

## install-skills.sh — 技能安装与同步

管理 `~/.ai/skills` 中两类技能的安装与同步：

- **受管技能**（仓库 `skills/` 下的目录）：在 `~/.ai/skills` 建 **symlink 指向仓库**，改仓库文件即生效
- **外部技能**（lark-*、从 ClawHub 市场下载的）：**不进 git**，物理目录 + tar 快照兜底（市场技能可能下架）

### 用法

```bash
./scripts/install-skills.sh            # 同步: 链接受管技能 + 刷新外部技能快照 (= 本机最新状态)
./scripts/install-skills.sh --restore  # 恢复: 链接受管技能 + 从快照恢复外部技能 (新机器首次安装)
```

### 同步方向（重要）

| 命令 | 方向 | 适用场景 |
|------|------|---------|
| `install`（默认） | 本机 → 快照 | 日常维护。装/改/删外部技能后跑一次，快照自动保持最新 |
| `--restore` | 快照 → 本机 | 新机器首次安装。一次性铺开外部技能 |

快照文件：`~/.ai/skills-external.tar.gz`（可用环境变量 `AI_SKILLS_SNAPSHOT` 覆盖路径）。

### 新机器安装流程

```bash
# 1. 克隆仓库（受管技能随代码到手）
git clone <ai-repo> && cd ai

# 2. 把旧机器的外部技能快照拷贝过来
scp old-machine:~/.ai/skills-external.tar.gz ~/.ai/

# 3. 恢复（链接受管技能 symlink + 解压外部技能快照）
./scripts/install-skills.sh --restore
```

### 日常维护

```bash
# 新增/修改/删除了外部技能后，同步快照（快照永远=本机状态，不会过期）
./scripts/install-skills.sh
```

### 注意事项

- 受管技能 symlink 指向仓库的**绝对路径**，仓库被移动后需重跑 install 修复
- 快照只包含外部技能（物理目录）；受管技能（symlink）自动排除，不会重复打包
- 如果 `~/.ai/skills` 中已有同名物理目录（外部技能），install 不会覆盖它