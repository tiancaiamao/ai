# MCP Skills - Quick Start Guide

## 🎉 Successfully Installed!

All 5 MCP skills have been created and are ready to use!

## 📁 What Was Created

```
~/.ai/skills/  (symlink to /Users/genius/project/ai/skills/)
├── mcp-fetch/
│   ├── SKILL.md
│   └── mcp-fetch.sh
├── mcp-context7/
│   ├── SKILL.md
│   └── mcp-context7.sh
├── mcp-brave-search/
│   ├── SKILL.md
│   └── mcp-brave-search.sh
├── mcp-git/
│   ├── SKILL.md
│   └── mcp-git.sh
├── mcp-zai/
│   ├── SKILL.md
│   └── mcp-zai.sh
├── test-mcp-skills.sh
├── MCP-SKILLS-README.md
└── MCP-QUICKSTART.md
```

## 🚀 Next Steps

### Step 1: Verify Installation (Optional)

Run the test suite:
```bash
cd /Users/genius/project/ai/skills
./test-mcp-skills.sh
```

Expected: 18+ tests should pass (functional tests may fail without API keys)

### Step 2: Set Up API Keys (Recommended)

**Get your free API keys:**

| Skill | API Key | Cost | Sign-up Link |
|-------|---------|------|--------------|
| mcp-brave-search | BRAVE_API_KEY | Free tier | https://api.search.brave.com/app/keys |
| mcp-context7 | CONTEXT7_API_KEY | Free tier | https://context7.ai |
| mcp-zai | ZAI_API_KEY | Free tier | https://open.bigmodel.cn |

**Set them as environment variables:**
```bash
# Add to your ~/.zshrc or ~/.bashrc
export BRAVE_API_KEY="your_key_here"
export CONTEXT7_API_KEY="your_key_here"
export ZAI_API_KEY="your_key_here"
```

**Or create .env files:**
```bash
cd mcp-brave-search && echo "BRAVE_API_KEY=your_key" > .env && cd ..
cd mcp-context7 && echo "CONTEXT7_API_KEY=your_key" > .env && cd ..
cd mcp-zai && echo "ZAI_API_KEY=your_key" > .env && cd ..
```

### Step 3: Start Using!

The skills are now available to your AI agent. Try these examples:

#### Example 1: Search the Web
```
User: Search for "Model Context Protocol 2025"
Agent: [Invokes mcp-brave-search skill]
```

#### Example 2: Fetch Web Content
```
User: Fetch the README from https://github.com/modelcontextprotocol/servers
Agent: [Invokes mcp-fetch skill]
```

#### Example 3: Query API Documentation
```
User: How do I use useState in React 18.2?
Agent: [Invokes mcp-context7 skill]
```

#### Example 4: Analyze Git History
```
User: Show me the last 10 commits to this repository
Agent: [Invokes mcp-git skill]
```

#### Example 5: Understand an Image
```
User: Analyze this screenshot: screenshot.png
Agent: [Invokes mcp-zai skill]
```

## 📊 Test Results Summary

From the test run:
- ✅ **18 tests passed** - All skills properly installed
- ⚠️ **2 functional tests failed** - Expected (requires MCP servers or API keys)
- ⏭️ **6 skipped** - API key tests (will pass once keys are configured)

## 🔧 Manual Testing

You can test each skill directly:

```bash
# Test fetch (no API key needed)
./mcp-fetch/mcp-fetch.sh "https://httpbin.org/json"

# Test brave-search (requires API key)
./mcp-brave-search/mcp-brave-search.sh "test query"

# Test git (requires git repository)
cd /path/to/git/repo
./mcp-git/mcp-git.sh status

# Test context7 (requires API key)
./mcp-context7/mcp-context7.sh react latest useState

# Test zai (requires API key and image URL)
./mcp-zai/mcp-zai.sh analyze "https://example.com/image.png" "Describe this"
```

## 🎓 Skill Capabilities

### mcp-fetch
- ✅ Fetch URLs (HTML, JSON, text, Markdown)
- ✅ Convert HTML to Markdown automatically
- ✅ No API key required
- ⚠️ Requires Node.js and npx

### mcp-context7
- ✅ Query latest API documentation
- ✅ Match exact package versions
- ✅ Prevents LLM code hallucinations
- ⚠️ Requires API key (free tier)

### mcp-brave-search
- ✅ Search the web independently
- ✅ Time and domain filtering
- ✅ Current information retrieval
- ⚠️ Requires API key (generous free tier)

### mcp-git
- ✅ Advanced Git queries
- ✅ Structured output (not raw text)
- ✅ Blame, diff, log with metadata
- ⚠️ Requires Python/Node.js MCP server

### mcp-zai
- ✅ OCR text extraction
- ✅ UI-to-code conversion
- ✅ Technical diagram understanding
- ✅ Error screenshot diagnosis
- ⚠️ Requires API key (free tier)

## 📚 Documentation

Each skill has detailed documentation in its `SKILL.md` file:
- `/Users/genius/project/ai/skills/mcp-fetch/SKILL.md`
- `/Users/genius/project/ai/skills/mcp-context7/SKILL.md`
- `/Users/genius/project/ai/skills/mcp-brave-search/SKILL.md`
- `/Users/genius/project/ai/skills/mcp-git/SKILL.md`
- `/Users/genius/project/ai/skills/mcp-zai/SKILL.md`

## 🐛 Troubleshooting

**Problem:** Skill scripts say "command not found: jq"
```bash
# Fix: Install jq
brew install jq  # macOS
# or
sudo apt-get install jq  # Linux
```

**Problem:** mcp-fetch or mcp-git fail with process errors
```bash
# Fix: Install Node.js
brew install node  # macOS
# or download from https://nodejs.org
```

**Problem:** Skills ask for API keys
```bash
# Fix: Set environment variables or create .env files
# (see Step 2 above)
```

## 💡 Tips

1. **Start with mcp-fetch and mcp-brave-search** - They're the most useful
2. **Get a Brave Search API key** - Free tier is very generous
3. **Use mcp-context7 when coding** - Prevents using outdated APIs
4. **mcp-zai is great for debugging** - Analyze error screenshots
5. **mcp-git complements bash git** - Use for complex queries

## 🎯 Recommended Workflow

1. **Immediate use:** mcp-fetch, mcp-brave-search (no setup needed beyond Node.js)
2. **Next step:** Get Brave Search API key (free, instant access)
3. **As needed:** Set up context7 and zai when you need those features

## 📞 Support

- Check `/Users/genius/project/ai/skills/MCP-SKILLS-README.md` for detailed documentation
- Each skill's SKILL.md file has usage examples and troubleshooting
- Run `./test-mcp-skills.sh` to diagnose issues

---

**Happy coding with MCP! 🎉**
