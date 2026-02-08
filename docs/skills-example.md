# 技能系统 (Skills System)

ai 现在完全支持 [Agent Skills](https://agentskills.io) 标准！

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
├── react-component.md           # 根级别 Markdown
├── test-driven-development/     # 子目录
│   └── SKILL.md                # 子目录中的 SKILL.md
└── api-integration/
    └── SKILL.md
```

## 示例技能

### 示例 1: React 组件生成器

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
6. Keep components under 200 lines

Example:
```tsx
interface ButtonProps {
  label: string;
  onClick: () => void;
  variant?: 'primary' | 'secondary';
}

export function Button({ label, onClick, variant = 'primary' }: ButtonProps) {
  return (
    <button
      className={`px-4 py-2 rounded ${variant === 'primary' ? 'bg-blue-500' : 'bg-gray-500'}`}
      onClick={onClick}
    >
      {label}
    </button>
  );
}
```
```

### 示例 2: 单元测试技能

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
6. Aim for >80% code coverage

Example:
```go
func TestAdd(t *testing.T) {
    tests := []struct {
        name     string
        a, b     int
        expected int
    }{
        {"positive numbers", 2, 3, 5},
        {"negative numbers", -2, -3, -5},
        {"zero", 0, 5, 5},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := Add(tt.a, tt.b)
            if result != tt.expected {
                t.Errorf("Add(%d, %d) = %d; want %d", tt.a, tt.b, result, tt.expected)
            }
        })
    }
}
```
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
The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>react-component</name>
    <description>Generate React components with TypeScript and Tailwind CSS</description>
    <location>/Users/username/.ai/skills/react-component.md</location>
  </skill>
  <skill>
    <name>test-driven-development</name>
    <description>Write comprehensive unit tests using Go testing framework</description>
    <location>/Users/username/.ai/skills/test-driven-development/SKILL.md</location>
  </skill>
</available_skills>
```

### 3. AI 使用技能

当用户请求与技能描述匹配的任务时：
1. AI 看到技能列表
2. AI 使用 `read` 工具加载完整的技能文件
3. AI 按照技能内容中的指令执行任务

示例对话：
```
User: Create a React button component

AI: I'll use the read tool to load the react-component skill...
[Reads /Users/username/.ai/skills/react-component.md]
AI: Based on the react-component skill, here's a TypeScript React component...
```

### 4. 禁用自动调用的技能

如果设置 `disable-model-invocation: true`：
- 技能不会出现在自动提示中
- 只能通过显式命令调用（例如 `/skill:api-integration`）

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

# 或使用子目录
mkdir -p ~/.ai/skills/my-skill
cat > ~/.ai/skills/my-skill/SKILL.md << 'EOF'
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

### 步骤 3: 测试技能

```
User: Help me with [task matching skill description]

AI: I'll load the my-skill skill...
[Reads skill file]
AI: Based on the my-skill skill, here's what I'll do...
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

### 技能出现在 AI 响应中但未生效？

- 检查描述是否足够清晰
- 确保 AI 有读取工具可用
- 尝试更具体地描述任务

## 相关资源

- [Agent Skills 官方规范](https://agentskills.io/specification)
- [Agent Skills 集成指南](https://agentskills.io/integrate-skills)
- [ai 配置文档](./config-example.md)

## 示例技能库

以下是一些常用的技能模板：

### Go API 技能

```markdown
---
name: go-api
description: Create RESTful APIs using Go and Gin framework
---

When creating a Go API:

1. Use Gin framework for routing
2. Follow project structure:
   - cmd/api/main.go
   - internal/handlers/
   - internal/services/
   - internal/models/
3. Use structured logging
4. Implement proper error handling
5. Add API documentation with Swagger
```

### Git 技能

```markdown
---
name: git-workflow
description: Follow Git best practices and common workflows
---

Git workflow guidelines:

1. Commit messages:
   - Use imperative mood ("Add feature" not "Added feature")
   - Limit to 50 characters for subject
   - Wrap body at 72 characters

2. Branch naming:
   - feature/feature-name
   - bugfix/bug-description
   - hotfix/urgent-fix

3. Before pushing:
   - Run tests
   - Format code
   - Check for linting errors
```

### 数据库技能

```markdown
---
name: database-schema
description: Design normalized database schemas with proper indexing
---

Database design principles:

1. Use appropriate normal forms (3NF typically)
2. Add indexes for:
   - Foreign keys
   - Frequently queried columns
   - Filter and join columns

3. Naming conventions:
   - Tables: snake_case plural (users, user_profiles)
   - Columns: snake_case (created_at, user_id)
   - Indexes: idx_table_column(s)

4. Always include:
   - Primary keys
   - Created/updated timestamps
   - Proper foreign key constraints
```
