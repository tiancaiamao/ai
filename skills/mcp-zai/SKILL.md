---
name: mcp-zai
description: Multi-modal image analysis using Z.AI's GLM-4V vision model. Provides OCR, UI-to-code conversion, technical diagram understanding, data visualization analysis, and error screenshot diagnosis.
allowed-tools: [bash]
disable-model-invocation: false
---

# MCP Z.AI Skill

This skill provides advanced image analysis capabilities using Z.AI's GLM-4V vision model through their MCP server.

## What This Skill Does

When you need to analyze images:
1. Takes an image URL or local file path
2. Sends the image to Z.AI's vision model for analysis
3. Returns detailed analysis based on the specified task type
4. Supports multiple analysis modes (OCR, UI-to-code, diagram understanding, etc.)

## When to Use This Skill

Use this skill when:
- Extracting text from images (OCR)
- Converting UI designs to code
- Understanding technical diagrams (architecture, flowcharts, UML)
- Analyzing data visualizations (charts, graphs, dashboards)
- Diagnosing error messages from screenshots
- Understanding visual content programmatically

## How It Works

Z.AI's MCP server provides specialized analysis tools:
- **OCR**: Extract text from images (screenshots, scanned documents)
- **UI to Code**: Generate frontend code from design mockups
- **Technical Diagrams**: Understand architecture diagrams, flowcharts, UML
- **Data Visualization**: Extract insights from charts and graphs
- **Error Diagnosis**: Analyze error screenshots and provide solutions
- **General Analysis**: Flexible image understanding

## Usage Examples

### Example 1: Extract Text from Screenshot (OCR)
```bash
mcp-zai.sh ocr "https://example.com/screenshot.png" "Extract all text from this image"
```

### Example 2: Convert UI Design to Code
```bash
mcp-zai.sh ui-to-code "https://example.com/design.png" "Describe the layout and components"
```

### Example 3: Understand Architecture Diagram
```bash
mcp-zai.sh diagram "https://example.com/architecture.png" "Explain this system architecture"
```

### Example 4: Analyze Chart
```bash
mcp-zai.sh chart "https://example.com/dashboard.png" "What trends do you see?"
```

### Example 5: Diagnose Error
```bash
mcp-zai.sh error "https://example.com/error.png" "What's wrong and how to fix it?"
```

### Example 6: General Analysis
```bash
mcp-zai.sh analyze "https://example.com/image.png" "Describe what you see"
```

## Analysis Types

The skill supports specialized analysis modes:

| Mode | Purpose | Best For |
|------|---------|----------|
| `ocr` | Extract text from images | Screenshots, scanned docs, code snippets |
| `ui-to-code` | Generate frontend code | Design mockups, wireframes, UI comps |
| `diagram` | Understand technical diagrams | Architecture, flowcharts, UML, ER diagrams |
| `chart` | Analyze data visualizations | Charts, graphs, dashboards, metrics |
| `error` | Diagnose errors | Error screenshots, stack traces, exception messages |
| `analyze` | General image understanding | Any visual content |

## API Key Setup

To use this skill, you need a Z.AI API key:

1. Get your API key from https://open.bigmodel.cn
2. Set it as an environment variable:
   ```bash
   export ZAI_API_KEY="your_api_key_here"
   ```

Or create `.env` file in this skill directory:
```
ZAI_API_KEY=your_api_key_here
```

## Command Syntax

```bash
mcp-zai.sh <mode> <image> <prompt>

Modes:
  ocr          Extract text from images
  ui-to-code   Convert UI designs to code
  diagram      Understand technical diagrams
  chart        Analyze data visualizations
  error        Diagnose error screenshots
  analyze      General image analysis

Arguments:
  mode         Analysis mode (see table above)
  image        Image URL or local file path
  prompt       Description of what to analyze or extract
```

## Image Sources

The skill supports:
- **Remote URLs**: Any publicly accessible image URL
- **Local files**: Absolute or relative paths to local images
- **Supported formats**: PNG, JPG, JPEG, WebP

## Implementation Details

The skill uses Z.AI's HTTP-based MCP API:
- Base URL: https://open.bigmodel.cn/api/mcp/
- Authentication: Bearer token in Authorization header
- Model: GLM-4V (advanced vision model)
- Method: POST with JSON payload containing image and prompt

## Response Format

The skill returns:
- Textual analysis based on the mode
- For OCR: Extracted text content
- For UI-to-code: Layout description and component suggestions
- For diagrams: Explanation of structure and relationships
- For charts: Data trends and insights
- For errors: Problem description and solutions

## Error Handling

The script will:
- Check for API key before making requests
- Validate image URL/file accessibility
- Handle network timeouts gracefully
- Report API errors clearly
- Return helpful error messages for common issues

## Benefits Over Built-in Tools

- **Multi-modal**: Adds vision capabilities to text-only agent
- **Specialized**: Different modes for different tasks
- **Accurate**: Uses state-of-the-art GLM-4V model
- **Flexible**: Works with URLs or local files
- **Integrated**: Direct HTTP API, no local dependencies

## Limitations

- Requires API key (free tier available)
- Rate limits may apply
- Requires internet connectivity
- Maximum image file size: 8MB
- Processing time depends on image complexity

## Use Cases

### Development
- Extract code from screenshots
- Convert design mockups to HTML/CSS
- Understand system architecture diagrams
- Analyze performance charts

### Documentation
- OCR scanned documents
- Extract text from presentation slides
- Process handwritten notes
- Archive visual information as text

### Debugging
- Analyze error screenshots
- Understand stack traces from images
- Get solutions from error messages
- Compare before/after states

## Why Z.AI?

Z.AI's GLM-4V model offers:
- **Strong OCR**: Excellent text extraction accuracy
- **Code Understanding**: Specialized for technical diagrams
- **UI Analysis**: Understands design patterns and components
- **Chinese Support**: Optimized for Chinese and English content
- **Accessibility**: Available in China (unlike some US providers)

## Tips for Best Results

1. **Be Specific**: Provide detailed prompts describing what you need
2. **High Quality**: Use clear, high-resolution images when possible
3. **Right Mode**: Choose the analysis mode that matches your task
4. **Multi-step**: For complex tasks, break into multiple analyses
5. **Context**: Include relevant context in your prompt

## Example Workflows

### Workflow 1: Design to Implementation
```bash
# 1. Analyze design
mcp-zai.sh ui-to-code design.png "Describe layout structure"

# 2. Extract specific component
mcp-zai.sh ui-to-code design.png "Focus on the navigation component"

# 3. Generate code based on analysis
# (Use the analysis to write actual code)
```

### Workflow 2: Debug from Screenshot
```bash
# Analyze error screenshot
mcp-zai.sh error screenshot.png "What's wrong and how to fix it?"

# Extract error text for searching
mcp-zai.sh ocr screenshot.png "Extract the exact error message"

# Use extracted text to search for solutions
mcp-brave-search.sh "[extracted error message]"
```

### Workflow 3: Document Architecture
```bash
# Understand the diagram
mcp-zai.sh diagram architecture.png "Explain the components and relationships"

# Extract labels and annotations
mcp-zai.sh ocr architecture.png "Extract all text labels"

# Document in markdown
# (Combine analyses to create documentation)
```

## Notes

- Z.AI is based in China and provides services optimized for Chinese users
- GLM-4V is a competitive alternative to GPT-4V
- Free tier has generous limits for development
- Consider implementing caching for repeated analyses
- Works well in combination with other MCP skills (fetch, search)
