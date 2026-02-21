---
name: mcp-fetch
description: Web content fetching tool. Retrieves web content (HTML, JSON, text) and automatically converts HTML to Markdown for easy consumption by the AI.
allowed-tools: [bash]
disable-model-invocation: false
---

# MCP Fetch Skill

This skill provides web content fetching capabilities using standard Unix tools (curl, jq, pandoc, lynx).

## What This Skill Does

When you need to fetch content from a URL:
1. Takes a URL and optional format parameter
2. Fetches the content using curl
3. Automatically detects content type (HTML, JSON, text, etc.)
4. Converts HTML to Markdown (when available)
5. Returns formatted, AI-friendly content

## When to Use This Skill

Use this skill when:
- You need to fetch web content from a specific URL
- You need to read documentation from a website
- You need to get data from a JSON API endpoint
- You need to retrieve raw text or Markdown from a URL
- **No MCP server needed** - uses standard Unix tools

## How It Works

The skill uses standard Unix tools:
- **curl**: Fetch content from URLs
- **jq**: Pretty-print JSON (optional)
- **pandoc**: Convert HTML to Markdown (optional, best quality)
- **lynx**: Convert HTML to text (fallback)
- **Auto-detection**: Smart format detection based on Content-Type

## Usage Examples

### Example 1: Fetch JSON API
```bash
mcp-fetch.sh "https://api.github.com/repos/modelcontextprotocol/servers"
```

### Example 2: Fetch Webpage (auto-convert to Markdown)
```bash
mcp-fetch.sh "https://example.com"
```

### Example 3: Fetch with specific format
```bash
mcp-fetch.sh "https://example.com/article.html" markdown
mcp-fetch.sh "https://api.example.com/data" json
```

## Command Syntax

```bash
mcp-fetch.sh <url> [format]

Arguments:
  url     URL to fetch (required, must start with http:// or https://)
  format  Output format (optional):
          - auto: Auto-detect based on Content-Type (default)
          - json: Output as-is (JSON)
          - markdown: Convert HTML to Markdown
          - text: Extract plain text
```

## Content Processing

### HTML Content
- **Preferred**: Uses `pandoc` for high-quality Markdown conversion
- **Fallback**: Uses `lynx -dump` for basic text extraction
- **Last resort**: Returns raw HTML

### JSON Content
- Pretty-prints with `jq` if available
- Returns raw JSON otherwise

### Other Content
- Returns as-is with Content-Type header shown

## Requirements

**Mandatory:**
- `curl` - Always required

**Optional (improves functionality):**
- `jq` - Pretty-print JSON (highly recommended)
- `pandoc` - Best HTML to Markdown conversion
- `lynx` - Fallback HTML text extraction

### Install Optional Tools

```bash
# macOS
brew install jq pandoc lynx

# Ubuntu/Debian
sudo apt-get install jq pandoc lynx-cur

# Check if installed
which jq pandoc lynx
```

## Error Handling

The script will:
- Validate URL format (must be http:// or https://)
- Check for required dependencies (curl)
- Return HTTP error codes if request fails
- Show Content-Type header for debugging

## Output Format

The script outputs:
1. Header information (Fetching URL, Content-Type)
2. Formatted content body
3. Exits with appropriate status code (0 = success, non-zero = error)

## Implementation Details

- **No MCP server needed** - Pure bash implementation
- **Temporary files** - Uses `/tmp/mcp-fetch-content*` (auto-cleaned)
- **Follows redirects** - Uses `curl -L` to handle redirects
- **Silent mode** - Uses `curl -s` to hide progress, only shows results

## Benefits Over MCP Server Version

- ✅ **No external dependencies** - Doesn't require Node.js/Python MCP servers
- ✅ **Faster** - No process spawning overhead
- ✅ **Simpler** - Easy to debug and modify
- ✅ **Reliable** - Uses battle-tested Unix tools
- ⚠️ **Less feature-rich** - No advanced MCP features like resource listing

## Limitations

- Cannot list available tools (MCP feature)
- No resource discovery (MCP feature)
- Requires optional tools for best HTML→Markdown conversion
- Basic text extraction compared to specialized MCP servers

## Use Cases

1. **API Integration**: Fetch data from REST APIs
2. **Documentation Reading**: Get docs from websites and convert to Markdown
3. **Web Research**: Retrieve web pages for analysis
4. **Data Extraction**: Get structured data from JSON endpoints

## Notes

- This is a **simplified version** focused on reliability
- For advanced MCP features, use the official `@modelcontextprotocol/server-fetch`
- Auto-detection works well for most common content types (HTML, JSON)
- Consider installing `pandoc` for best HTML→Markdown conversion quality

## Examples

### Fetch GitHub API
```bash
mcp-fetch.sh "https://api.github.com/users/modelcontextprotocol"
```

### Fetch Documentation
```bash
mcp-fetch.sh "https://modelcontextprotocol.io/"
```

### Fetch with Format Specified
```bash
mcp-fetch.sh "https://httpbin.org/json" json
```
---
allowed-tools: [bash]
