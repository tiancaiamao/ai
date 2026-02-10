# 技能系统 (Skills System)

ai 完全支持 [Agent Skills](https://agentskills.io) 标准！

## 什么是技能？

技能是一种为 AI Agent 提供特定任务指令的机制。技能是 Markdown 文件，包含：
- YAML frontmatter（元数据）
- 任务描述
- 执行指南

## 技能文件格式

### 基本结构

```markdown
---
name: my-skill
description: A brief description of what this skill does
---

This is the skill content that provides instructions to the AI.
It can contain multiple paragraphs, code examples, and step-by-step guides.
```

### Frontmatter 字段

| 字段 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `name` | string | 是 | 技能名称（必须与目录名匹配） |
| `description` | string | 是 | 技能描述（最大 1024 字符） |
| `license` | string | 否 | 许可证信息 |
| `compatibility` | string | 否 | 兼容性说明 |
| `allowed-tools` | array | 否 | 允许使用的工具列表 |
| `disable-model-invocation` | bool | 否 | 禁用模型自动调用（仅显式调用） |

### 技能命名规则

- 只能包含小写字母、数字和连字符（`a-z`, `0-9`, `-`）
- 不能以连字符开头或结尾
- 不能包含连续连字符
- 最大长度 64 字符
- **必须**与父目录名称匹配

## 技能发现

ai 会自动从以下位置加载技能：

1. **全局技能**: `~/.ai/skills/`
2. **项目技能**: `.ai/skills/`（当前工作目录）
3. **显式路径**: 通过配置指定

### 发现规则

- 根目录中的直接 `.md` 文件
- 子目录中的 `SKILL.md` 文件

示例目录结构：
```
~/.ai/skills/
├── react-component.md
├── test-driven-development/
│   └── SKILL.md
└── api-integration/
    └── SKILL.md
```

## 示例技能

### React 组件生成器

**文件**: `~/.ai/skills/react-component.md`

```markdown
---
name: react-component
description: Generate React components with TypeScript and Tailwind CSS
---

When asked to create a React component:

1. Use TypeScript with strict type checking
2. Use Tailwind CSS for styling
3. Follow these naming conventions:
   - Component files: PascalCase (e.g., UserProfile.tsx)
   - CSS classes: kebab-case (e.g., user-profile)
4. Include props interface with JSDoc comments
5. Use functional components with hooks
```

### 单元测试技能

**文件**: `~/.ai/skills/test-driven-development/SKILL.md`

```markdown
---
name: test-driven-development
description: Write comprehensive unit tests using Go testing framework
---

When writing tests:

1. Use table-driven tests for multiple scenarios
2. Follow AAA pattern: Arrange, Act, Assert
3. Use descriptive test names
4. Mock external dependencies
5. Test both success and failure cases
```

## 技能的工作原理

### 1. 自动加载

ai 启动时会自动加载所有技能文件：
- 扫描 `~/.ai/skills/` 和 `.ai/skills/`
- 解析 frontmatter
- 验证名称和描述
- 报告任何诊断警告/错误

### 2. 系统提示集成

技能自动被格式化为 XML 并添加到系统提示中：

```xml
<available_skills>
  <skill>
    <name>react-component</name>
    <description>Generate React components...</description>
  </skill>
</available_skills>
```

### 3. AI 使用技能

当用户请求与技能描述匹配的任务时：
1. AI 看到技能列表
2. AI 使用 `read` 工具加载完整的技能文件
3. AI 按照技能内容中的指令执行任务

## 创建自定义技能

### 步骤 1: 创建技能文件

```bash
# 全局技能（所有项目可用）
mkdir -p ~/.ai/skills/my-skill
cat > ~/.ai/skills/my-skill.md << 'EOF'
---
name: my-skill
description: My custom skill description
---

Your skill instructions here...
EOF
```

### 步骤 2: 验证技能

```bash
# 启动 ai，检查日志
ai

# 应该看到类似输出：
# [ai] Loaded 1 skills
# [ai]   - my-skill: My custom skill description
```

## 技能诊断

ai 会在启动时报告技能加载问题：

- **警告**: 非致命问题（如未知 frontmatter 字段）
- **错误**: 致命问题（如缺少描述）

示例输出：
```
[ai] Skill loading: 2 diagnostics
[ai]   [warning] /path/to/skill.md: unknown frontmatter field "custom-field"
[ai]   [error] /path/to/bad-skill.md: description is required
```

## 技能最佳实践

### ✅ DO

- **使用描述性名称**: `react-component` 而不是 `rc`
- **提供清晰描述**: 在 50-100 字符内描述技能用途
- **包含示例**: 在技能内容中提供代码示例
- **保持专注**: 每个技能专注于一个特定任务
- **版本控制**: 将技能文件纳入版本控制（项目技能）

### ❌ DON'T

- **创建通用技能**: 避免过于宽泛的描述
- **过度嵌套**: 避免深层目录结构
- **忽略验证**: 注意命名规则和描述长度
- **硬编码路径**: 使用相对路径或占位符

## 技能与配置的对比

| 特性 | 技能 | 配置文件 |
|------|------|----------|
| **用途** | 任务指令 | 系统设置 |
| **格式** | Markdown + YAML | JSON |
| **位置** | ~/.ai/skills/ 或 .ai/skills/ | ~/.ai/config.json |
| **作用域** | 特定任务 | 全局行为 |
| **更新频率** | 经常（添加新技能） | 很少（设置一次） |

## 故障排除

### 技能没有加载？

1. 检查文件位置：
   ```bash
   ls -la ~/.ai/skills/
   ls -la .ai/skills/
   ```

2. 验证文件格式：
   ```bash
   # 检查 frontmatter
   head -5 ~/.ai/skills/your-skill.md
   ```

3. 检查日志：
   ```bash
   ai 2>&1 | grep -i skill
   ```

### 名称验证失败？

确保：
- 目录名与 `name` 字段匹配
- 只使用小写字母、数字和连字符
- 不以连字符开头或结尾
- 不包含连续连字符

## 相关资源

- [Agent Skills 官方规范](https://agentskills.io/specification)
- [Agent Skills 集成指南](https://agentskills.io/integrate-skills)
- [ai 配置文档](./config-example.md)
