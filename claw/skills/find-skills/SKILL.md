---
name: find-skills
description: 搜索和安装技能。当用户问"有没有 X 的 skill"、"搜索 skill"、"安装 skill"时使用。
version: 2.0.0
author: Ducc
metadata:
  openclaw:
    emoji: 🔍
    always: false
---

# Find Skills

从 ClawHub 技能市场搜索和安装技能。

## 使用方式

直接跟 Agent 说：

```
"有没有 GitHub 操作的 skill？"
"搜索网页抓取相关的 skill"
"安装 crawl4ai skill"
```

## 搜索技能

使用 ClawHub API 搜索：

```bash
# 搜索 GitHub 相关技能
curl -s "https://clawhub.ai/api/v1/search?q=github&limit=5" | jq .
```

返回结果包含：
- `slug` - 技能唯一标识
- `displayName` - 显示名称
- `summary` - 描述
- `version` - 版本号
- `score` - 相关度评分

## 安装技能

找到想要的技能后，下载并解压：

```bash
# 下载技能 ZIP
curl -L -o /tmp/skill.zip "https://clawhub.ai/api/v1/download?slug=<slug>"

# 解压到技能目录
unzip -o /tmp/skill.zip -d ~/.aiclaw/skills/<slug>

# 刷新技能列表
/skills reload
```

## 示例对话

```
用户: 有没有可以帮我操作 GitHub PR 的 skill？

Agent: [搜索 ClawHub]
找到了 3 个相关技能：

1. **github** v1.2.0 (评分: 0.95)
   GitHub PR/Issue/Branch 操作

2. **github-actions** v0.8.0 (评分: 0.72)
   CI/CD 工作流管理

要安装哪个？

用户: 安装 github

Agent: [下载并安装]
正在安装 github 技能...
✓ 已安装到 ~/.aiclaw/skills/github

执行 /skills reload 加载新技能...

用户: /skills reload

Agent: Reloaded 15 skills
```

## 技能目录

- `~/.aiclaw/skills/` - 用户安装的技能
- ClawHub: https://clawhub.ai - 默认技能市场

## 命令

| 命令 | 说明 |
|------|------|
| `/skills` | 列出已加载的技能 |
| `/skills list` | 同上 |
| `/skills reload` | 热加载新安装的技能 |

## 手动安装

从 Git 仓库安装：

```bash
git clone https://github.com/user/skill-repo ~/.aiclaw/skills/skill-name
/skills reload
```

## 创建自定义技能

参考 `skill-creator` 技能了解如何创建自己的技能。