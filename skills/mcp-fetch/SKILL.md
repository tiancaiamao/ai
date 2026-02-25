---
name: mcp-fetch
description: Web content fetching tool. Retrieves web content (HTML, JSON, text) and automatically converts HTML to Markdown for easy consumption by the AI.
allowed-tools: [bash]
disable-model-invocation: false
---

# MCP Fetch Skill

This skill provides web content fetching capabilities using standard Unix tools (curl, jq, pandoc, lynx).

**🎯 This is the recommended web fetching skill for this project.** (web-fetch was removed due to macOS compatibility issues)

## What This Skill Does

When you need to fetch content from a URL:
1. Takes a URL and optional format parameter
2. Fetches the content using curl with proper headers
3. Automatically detects content type (HTML, JSON, text, etc.)
4. Converts HTML to readable text/Markdown
5. Returns formatted, AI-friendly content

## When to Use This Skill

Use this skill when:
- You need to fetch web content from a specific URL
- You need to read documentation from a website
- You need to get data from a JSON API endpoint
- You need to retrieve text from a URL for analysis
- **No MCP server needed** - uses standard Unix tools

## How It Works

The skill uses standard Unix tools:
- **curl**: Fetch content from URLs with User-Agent header and timeout
- **jq**: Pretty-print JSON (optional)
- **pandoc**: Convert HTML to Markdown (optional, best quality)
- **lynx**: Convert HTML to text (fallback)
- **Auto-detection**: Smart format detection based on Content-Type

## Usage Examples

### Example 1: Fetch JSON API
```bash
/Users/genius/.ai/skills/mcp-fetch/mcp-fetch.sh "https://api.github.com/repos/golang/go" json
```

### Example 2: Fetch Webpage (auto-convert to text)
```bash
/Users/genius/.ai/skills/mcp-fetch/mcp-fetch.sh "https://example.com"
```

### Example 3: Save content to file
```bash
/Users/genius/.ai/skills/mcp-fetch/mcp-fetch.sh "https://example.com" auto /tmp/example.txt
```

### Example 4: Set custom timeout
```bash
FETCH_TIMEOUT=60 /Users/genius/.ai/skills/mcp-fetch/mcp-fetch.sh "https://slow-site.com"
```

## Command Syntax

```bash
mcp-fetch.sh <url> [format] [output_file]

Arguments:
  url          URL to fetch (required, must start with http:// or https://)
  format       Output format (optional):
               - auto: Auto-detect based on Content-Type (default)
               - json: Output as-is with pretty-printing
               - markdown: Convert HTML to Markdown
               - text: Extract plain text
  output_file  Save to file instead of stdout (optional)

Environment Variables:
  FETCH_TIMEOUT  Request timeout in seconds (default: 30)
```

## New Features (Enhanced)

### User-Agent Header
Uses a proper User-Agent header to avoid being blocked by websites:
```
User-Agent: Mozilla/5.0 (compatible; AI-Agent/1.0; +https://github.com/modelcontextprotocol)
```

### Configurable Timeout
Prevents hanging indefinitely on slow or unresponsive servers:
- Default: 30 seconds
- Customizable via `FETCH_TIMEOUT` environment variable

### Save to File
Optionally save fetched content to a file instead of printing to stdout.

### Better HTML Processing
- Removes script and style tags completely
- Handles multi-line HTML tags correctly
- Decodes HTML entities properly
- Filters out CSS code and noise

## Content Processing

### HTML Content
- **Preferred**: Uses `pandoc` for high-quality Markdown conversion
- **Fallback**: Uses `lynx -dump` for basic text extraction
- **Last resort**: Enhanced HTML tag stripping with entity decoding

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
- Timeout after configured time (default 30s)
- Show Content-Type header for debugging

## Output Format

The script outputs:
1. Header information (Fetching URL, Content-Type)
2. Formatted content body
3. File save confirmation (if output_file specified)
4. Exits with appropriate status code (0 = success, non-zero = error)

## Implementation Details

- **No MCP server needed** - Pure bash implementation
- **Temporary files** - Uses mktemp (auto-cleaned on exit)
- **Follows redirects** - Uses `curl -L` to handle redirects
- **Silent mode** - Uses `curl -s` to hide progress, only shows results
- **Timeout protection** - `--max-time` prevents hanging

## Benefits Over web-fetch

The `web-fetch` skill was removed because:
- ❌ Used `grep -P` (Perl regex) which is incompatible with macOS/BSD grep
- ❌ Script was not executable by default
- ❌ Poor text formatting (words run together)
- ❌ No JSON API support

`mcp-fetch` advantages:
- ✅ POSIX-compatible (works on macOS and Linux)
- ✅ Multi-format support (JSON + HTML)
- ✅ Better error handling
- ✅ Proper timeout configuration
- ✅ Save-to-file support
- ✅ User-Agent header for better compatibility
- ✅ Enhanced HTML to text conversion

## Limitations

- Requires JavaScript execution for client-rendered pages (Next.js, React, etc.)
- Server-rendered pages only (most documentation sites work fine)
- Optional tools needed for best HTML→Markdown conversion
- Basic text extraction compared to specialized MCP servers

## Use Cases

1. **API Integration**: Fetch data from REST APIs
2. **Documentation Reading**: Get docs from websites and convert to Markdown
3. **Web Research**: Retrieve web pages for analysis
4. **Data Extraction**: Get structured data from JSON endpoints
5. **Content Saving**: Fetch and save content to files

## Examples

### Fetch GitHub API
```bash
mcp-fetch.sh "https://api.github.com/users/modelcontextprotocol" json
```

### Fetch Documentation
```bash
mcp-fetch.sh "https://modelcontextprotocol.io/"
```

### Fetch and Save
```bash
mcp-fetch.sh "https://example.com" auto /tmp/content.txt
```

### Custom Timeout
```bash
FETCH_TIMEOUT=60 mcp-fetch.sh "https://example.com"
```

## Migration from web-fetch

If you were using `web-fetch`, switch to `mcp-fetch`:

```bash
# Old (broken on macOS)
web-fetch/scripts/fetch.sh "https://example.com"

# New (works everywhere)
mcp-fetch/mcp-fetch.sh "https://example.com"

# With format
mcp-fetch/mcp-fetch.sh "https://example.com" auto /tmp/output.txt
```

## Notes

- This is the **recommended web fetching skill** for this project
- For advanced MCP features (resource listing, etc.), use the official `@modelcontextprotocol/server-fetch`
- Auto-detection works well for most common content types (HTML, JSON)
- Consider installing `pandoc` for best HTML→Markdown conversion quality
- The script handles most server-rendered HTML content well
- Client-rendered content (JavaScript-heavy sites) won't work