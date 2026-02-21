---
name: mcp-brave-search
description: Web search using Brave Search API. Provides independent search capabilities that don't rely on Google, with support for time filtering, domain filtering, and content summaries. Essential for retrieving current information beyond the LLM's knowledge cutoff.
allowed-tools: [bash]
disable-model-invocation: false
---

# MCP Brave Search Skill

This skill provides web search capabilities using Brave Search API, offering an independent alternative to Google Search.

## What This Skill Does

When you need to search the web:
1. Takes your search query and optional filters
2. Queries Brave Search API
3. Returns structured search results with titles, URLs, snippets, and metadata
4. Provides summarized content for efficient information retrieval

## When to Use This Skill

Use this skill when:
- You need current information beyond the LLM's training data
- Researching recent developments, news, or trends
- Finding documentation or tutorials
- Looking up specific technical questions
- Verifying facts or getting multiple perspectives

## How It Works

Brave Search API offers:
- **Independent indexing**: Not dependent on Google's search results
- **Privacy-focused**: No tracking or personalized results
- **Global coverage**: Searches the entire web
- **Rich snippets**: Provides informative summaries
- **Time filtering**: Restrict results to recent time periods
- **Domain filtering**: Search specific websites or exclude domains

## Usage Examples

### Example 1: Basic Search
```bash
mcp-brave-search.sh "Model Context Protocol"
```

### Example 2: Search with Time Filter
```bash
mcp-brave-search.sh "AI agents 2025" --time-recent "oneWeek"
```

### Example 3: Search Specific Domain
```bash
mcp-brave-search.sh "MCP servers" --domain "github.com"
```

### Example 4: Exclude Domains
```bash
mcp-brave-search.sh "climate change" --exclude-domain "twitter.com"
```

## API Key Setup

To use this skill, you need a Brave Search API key:

1. Get your free API key from https://api.search.brave.com/app/keys
2. Set it as an environment variable:
   ```bash
   export BRAVE_API_KEY="your_api_key_here"
   ```

Or create `.env` file in this skill directory:
```
BRAVE_API_KEY=your_api_key_here
```

## Command Options

```
mcp-brave-search.sh <query> [options]

Options:
  --time-recent <filter>   Time filter: oneDay, oneWeek, oneMonth, oneYear, noLimit
  --domain <domain>        Restrict search to specific domain
  --exclude-domain <domain> Exclude specific domain from results
  --count <number>         Number of results (default: 10, max: 20)
  --offset <number>        Pagination offset (default: 0)
```

## Search Filters

### Time Filters
- `oneDay` - Results from the last 24 hours
- `oneWeek` - Results from the last week
- `oneMonth` - Results from the last month
- `oneYear` - Results from the last year
- `noLimit` - No time restriction (default)

### Domain Filtering
- `--domain example.com` - Only search within example.com
- `--exclude-domain twitter.com` - Exclude twitter.com from results

## Response Format

The script returns search results in a structured format:

```
# Search Results for: "your query"

## Result 1: [Title]
**URL:** https://example.com/page
**Snippet:** Brief description of the page content...

## Result 2: [Title]
...
```

## Implementation Details

The skill uses Brave Search Web API:
- Endpoint: https://api.search.brave.com/res/v1/web/search
- Authentication: Bearer token in Authorization header
- Method: GET request with query parameters
- Response: JSON with search results and metadata

## Error Handling

The script will:
- Check for API key before making requests
- Return clear error messages for invalid parameters
- Handle rate limits gracefully (429 errors)
- Report network or API errors

## Benefits Over Built-in Tools

- **Current Information**: Unlike static documentation, gets real-time web results
- **Broad Coverage**: Searches the entire web, not just specific sites
- **Independent**: Not dependent on Google or any single search provider
- **Privacy-Focused**: Brave Search doesn't track searches
- **Flexible**: Supports advanced filtering options

## Limitations

- Requires free API key (generous rate limits)
- Rate limits apply (100 requests per second on free tier)
- Requires internet connectivity
- Results depend on Brave's search index

## Why Brave Search?

Brave Search offers several advantages:
- **Independence**: Not reliant on Google's monopoly
- **Privacy**: No tracking, no profiling
- **Quality**: High-quality, relevant results
- **API Access**: Straightforward API with good documentation
- **Free Tier**: Generous free tier for development

## Use Cases

1. **Research**: Look up recent articles, papers, or documentation
2. **Troubleshooting**: Find solutions to specific error messages
3. **News**: Get current events and breaking news
4. **Comparisons**: Research multiple sources on a topic
5. **Verification**: Fact-check claims or statements

## Notes

- Brave Search API has a generous free tier (100 requests/second)
- Consider implementing caching for frequently searched queries
- Results may vary from Google Search due to different indexing
- Excellent complement to the fetch skill for web research
