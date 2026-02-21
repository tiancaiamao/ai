# MCP Skills Collection

This directory contains 5 Model Context Protocol (MCP) skills that extend the AI agent's capabilities through bash-based adapters.

## Skills Overview

### 1. mcp-fetch
**Purpose:** Web content fetching (HTML, JSON, text, Markdown)
- Fetches URLs and converts HTML to Markdown
- Supports multiple content formats
- **No API key required**

### 2. mcp-context7
**Purpose:** Query latest API documentation to avoid LLM hallucinations
- Real-time access to current library documentation
- Matches exact package versions
- **Requires API key** from https://context7.ai

### 3. mcp-brave-search
**Purpose:** Web search using Brave Search API
- Independent search (not Google-dependent)
- Time filtering and domain filtering
- **Requires API key** from https://api.search.brave.com/app/keys (free tier)

### 4. mcp-git
**Purpose:** Advanced Git operations with structured output
- Complex Git queries and analysis
- Blame, diff, log with metadata
- **No API key required** (requires Python/Node.js)

### 5. mcp-zai
**Purpose:** Multi-modal image analysis using GLM-4V
- OCR, UI-to-code, diagram understanding
- Error diagnosis from screenshots
- **Requires API key** from https://open.bigmodel.cn

## Quick Start

### 1. Test Installation

Run the test suite to verify all skills are properly installed:

```bash
./test-mcp-skills.sh
```

Expected results:
- ✅ All directory structure tests pass
- ✅ All SKILL.md files valid
- ✅ All scripts executable
- ⚠️ Functional tests may fail (requires API keys or MCP servers)

### 2. Set Up API Keys (Optional)

For skills that require API keys, you have two options:

**Option A: Environment Variables**
```bash
export CONTEXT7_API_KEY="your_key_here"
export BRAVE_API_KEY="your_key_here"
export ZAI_API_KEY="your_key_here"
```

**Option B: .env Files**
Create `.env` file in each skill directory:
```bash
# mcp-context7/.env
CONTEXT7_API_KEY=your_key_here

# mcp-brave-search/.env
BRAVE_API_KEY=your_key_here

# mcp-zai/.env
ZAI_API_KEY=your_key_here
```

### 3. Use the Skills

Each skill can be invoked directly or through the AI agent:

#### Direct Invocation

```bash
# Fetch web content
./mcp-fetch/mcp-fetch.sh "https://example.com"

# Query API documentation
./mcp-context7/mcp-context7.sh react 18.2.0 useState

# Search the web
./mcp-brave-search/mcp-brave-search.sh "Model Context Protocol"

# Git operations (must be in a git repository)
./mcp-git/mcp-git.sh log --max-count 5

# Analyze images
./mcp-zai/mcp-zai.sh ocr "screenshot.png" "Extract all text"
```

#### Through AI Agent

The skills will be automatically available to the AI agent, which can invoke them based on task requirements.

## API Key Setup Guide

### Context7 (Optional)
1. Visit https://context7.ai
2. Sign up for free account
3. Get API key from dashboard
4. Set as environment variable or in .env file

### Brave Search (Recommended)
1. Visit https://api.search.brave.com/app/keys
2. Sign up for free account (generous free tier)
3. Get API key
4. Set as environment variable or in .env file

### Z.AI (Optional)
1. Visit https://open.bigmodel.cn
2. Register for account
3. Get API key from console
4. Set as environment variable or in .env file

## Troubleshooting

### mcp-fetch fails with "Failed to get response"
- **Cause:** Node.js or npx not installed
- **Fix:** Install Node.js from https://nodejs.org

### mcp-git fails with "Failed to get response"
- **Cause:** Python MCP server not installed
- **Fix:** Run `pip install 'mcp[cli]'` or `npm install -g @modelcontextprotocol/server-git`

### Skills report missing API keys
- **Cause:** API keys not configured
- **Fix:** Set environment variables or create .env files (see above)

### "jq: command not found"
- **Cause:** Missing JSON parser
- **Fix:** Install jq:
  - macOS: `brew install jq`
  - Ubuntu/Debian: `sudo apt-get install jq`
  - CentOS/RHEL: `sudo yum install jq`

## Implementation Notes

### Architecture
All skills follow the same pattern:
1. **SKILL.md**: Markdown file with YAML frontmatter describing the skill
2. **<skill>.sh**: Bash script that implements the MCP client
3. **Communication**: JSON-RPC 2.0 over stdio or HTTP API calls

### Limitations
- **Performance overhead**: Each skill invocation spawns a new process
- **No connection pooling**: MCP servers are restarted on each call
- **Dependencies**: Some skills require Node.js, Python, or external tools
- **Rate limits**: API-based skills may have rate limits

### Best Practices
- Use **mcp-fetch** for one-off URL fetching
- Use **mcp-brave-search** for current information research
- Use **mcp-context7** when writing code using external libraries
- Use **mcp-git** for complex Git queries (not basic operations)
- Use **mcp-zai** for image-related tasks

## Contributing

To add new MCP skills:

1. Create a new directory: `mcp-<name>/`
2. Create `SKILL.md` with proper frontmatter
3. Create `mcp-<name>.sh` executable script
4. Add tests to `test-mcp-skills.sh`
5. Update this README

## Resources

- [MCP Specification](https://modelcontextprotocol.io)
- [MCP Official Servers](https://github.com/modelcontextprotocol/servers)
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [Anthropic Claude Desktop](https://claude.ai/download)

## License

These skills are part of the AI project and follow the same license.
