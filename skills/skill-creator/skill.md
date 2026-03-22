---
name: skill-creator
description: Create or update AgentSkills. Use when designing, structuring, or packaging skills with scripts, references, and assets.
---

# Skill Creator

This skill provides guidance for creating effective skills.

## Core Philosophy: TDD for Process Documentation

**Writing skills IS Test-Driven Development applied to process documentation.**

Same Iron Law: No skill without failing test first.
Same cycle: RED (baseline) → GREEN (write skill) → REFACTOR (close loopholes).
Same benefits: Better quality, fewer surprises, bulletproof results.

## About Skills

Skills are markdown files with YAML frontmatter that extend Claude's capabilities by providing specialized knowledge, tools, or workflows.

### Skill Directory Structure

```
skill-name/
├── SKILL.md           # Required: Main skill definition
├── scripts/           # Optional: Helper scripts
│   └── helper.sh
├── references/        # Optional: Reference documents
│   └── api-docs.md
└── assets/           # Optional: Static files
    └── template.json
```

### SKILL.md Format

```markdown
---
name: skill-name
description: One-line description of what this skill does
license: MIT
---

# Skill Title

## Overview
Brief explanation of the skill's purpose and when to use it.

## Instructions
Detailed guidance for using the skill.

## Examples
Concrete usage examples.
```

## Creating Skills: The TDD Way

### Step 1: RED - Establish Baseline (Find the Gap)

Before writing any skill content:

1. **Identify the problem**: What task is Claude struggling with?
2. **Document the gap**: What's missing? What context would help?
3. **Create a test case**: A real scenario where the skill should help

**Ask yourself:**
- What specific task should be easier with this skill?
- What mistakes does Claude make without this skill?
- What's the minimum information needed to succeed?

Without a clear failing case, you don't know what problem you're solving.

### Step 2: GREEN - Write the Skill

Now write the skill to solve the identified problem:

1. Create directory: `skill-name/SKILL.md`
2. Add YAML frontmatter with name and description
3. Write the minimum content to pass your test case
4. Test with the real scenario

**Required sections:**
- **Overview**: What this skill does and when to use it
- **Instructions**: Step-by-step guidance
- **Examples**: At least one concrete usage example

**Good skill content:**
- Direct commands over narrative
- Patterns table for quick reference
- Code examples with comments
- Common mistakes section

**Avoid:**
- Long narrative paragraphs
- Vague instructions like "be thoughtful"
- Missing concrete examples

### Step 3: REFACTOR - Close Loopholes

After testing:

1. **Where did Claude still struggle?**
2. **What edge cases weren't covered?**
3. **What instructions were ignored or misinterpreted?**

Close the loopholes:
- Add missing patterns
- Strengthen vague instructions
- Add anti-patterns to avoid
- Include decision trees for complex choices

## Skill Components

### YAML Frontmatter

Required fields:
- `name`: Skill identifier (lowercase, hyphens)
- `description`: One-line summary for discovery

Optional fields:
- `license`: License type (e.g., MIT)
- `tools`: Tool whitelist for restricted skills

### Content Sections

| Section | Purpose | Required |
|---------|---------|----------|
| Overview | What/When | Yes |
| Instructions | How | Yes |
| Examples | Concrete usage | Yes |
| Patterns | Quick reference table | Recommended |
| Anti-Patterns | What to avoid | Recommended |
| Common Mistakes | Troubleshooting | Optional |
| References | Links to docs | Optional |

### Supporting Files

Use supporting files for:
- **Scripts**: Executable helpers (must be in `scripts/`)
- **References**: Heavy documentation (must be in `references/`)
- **Templates**: Reusable templates (must be in `assets/`)

Keep SKILL.md focused on instructions, not dumping grounds for documentation.

## Discovery Workflow

How future Claude finds your skill:

1. **Encounters problem** ("tests are flaky")
2. **Finds SKILL** (description matches)
3. **Scans overview** (is this relevant?)
4. **Reads patterns** (quick reference table)
5. **Loads example** (only when implementing)

**Optimize for this flow** - put searchable terms early and often.

## Validation Checklist

Before deploying any skill:

**Structure:**
- [ ] SKILL.md exists in skill-name/ directory
- [ ] YAML frontmatter with name and description
- [ ] Description is one line, under 100 characters
- [ ] No narrative storytelling
- [ ] Supporting files only for tools or heavy reference

**Content:**
- [ ] Overview section explains what and when
- [ ] Instructions are actionable (not vague)
- [ ] At least one concrete example
- [ ] Common mistakes section (if complex)
- [ ] Patterns table for quick reference (if applicable)

**TDD:**
- [ ] Tested with real scenario
- [ ] Identified gaps have been addressed
- [ ] Edge cases documented

## Packaging Skills

### Step 5: Package (Optional)

If you want to share skills, use the packaging script:

```bash
scripts/package_skill.py <path/to/skill-folder>
```

Optional output directory specification:

```bash
scripts/package_skill.py <path/to/skill-folder> ./dist
```

The packaging script will:

1. **Validate** the skill automatically, checking:
   - YAML frontmatter format and required fields
   - Skill naming conventions and directory structure
   - Description completeness and quality
   - File organization and resource references

2. **Package** the skill if validation passes, creating a .skill file named after the skill (e.g., `my-skill.skill`) that includes all files and maintains the proper directory structure for distribution. The .skill file is a zip file with a .skill extension.

If validation fails, the script will report the errors and exit without creating a package. Fix any validation errors and run the packaging command again.

### Step 6: Iterate

After testing the skill, users may request improvements. Often this happens right after using the skill, with fresh context of how the skill performed.

**Iteration workflow:**

1. Use the skill on real tasks
2. Notice struggles or inefficiencies
3. Identify how SKILL.md or bundled resources should be updated
4. Implement changes and test again

## The Bottom Line

**Creating skills IS TDD for process documentation.**

If you follow TDD for code, follow it for skills. It's the same discipline applied to documentation.

No skill without failing test first. RED → GREEN → REFACTOR.