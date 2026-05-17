# pkg/truncate

Text truncation with UTF-8 safety, preserving head and tail content.

## Overview

Provides safe text truncation for tool outputs and long messages. The truncation preserves both the beginning and end of the text with a marker indicating how many tokens were removed.

## API

### Truncate

```go
func Truncate(text string, maxChars int) string
```

Truncates `text` to fit within `maxChars` bytes with:
- 50/50 split between prefix and suffix
- UTF-8 boundary safety (never splits a multi-byte character)
- Truncation marker showing approximate removed token count

Example output:
```
[first 500 chars]...[~1250 tokens truncated]...[last 500 chars]
```

### TruncateWithHeadTail

```go
func TruncateWithHeadTail(text string) string
```

Convenience wrapper using default limits (1000 head + 1000 tail + 500 marker budget).

### EstimateTokens

```go
func ApproxTokenCount(text string) int
```

Rough token estimate (~4 chars per token) for truncation markers.

## UTF-8 Safety

The `splitString` function ensures truncation boundaries never split a multi-byte UTF-8 sequence:

```go
func splitString(s string, beginningBytes, endBytes int) (removedTokens int, prefix, suffix string)
```

If a split would land inside a multi-byte character, it backs up to the previous valid boundary.

## Key Files

| File | Description |
|------|-------------|
| `truncate.go` | `Truncate()`, `TruncateWithHeadTail()`, `splitString()` |
| `estimate.go` | `ApproxTokenCount()` |