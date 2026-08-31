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
[first N chars]…1250 tokens truncated…[last N chars]
```

### TruncateString

```go
func TruncateString(s string, maxLen int) string
```

Truncates `s` to at most `maxLen` bytes, appending `"..."` if truncated.

### TrimRunes

```go
func TrimRunes(s string, limit int) string
```

Trims `s` to at most `limit` runes (Unicode code points). If `limit <= 0`, `s` is returned unchanged.

### Token Estimation

```go
func ApproxTokenCount(text string) int   // ~4 chars per token
func CharsToTokens(chars int) int        // Convert char count to token count
func TokensToChars(tokens int) int       // Convert token count to char count
```

## UTF-8 Safety

The `splitString` function ensures truncation boundaries never split a multi-byte UTF-8 sequence:

```go
func splitString(s string, beginningBytes, endBytes int) (removedTokens int, prefix, suffix string)
```

If a split would land inside a multi-byte character, it backs up to the previous valid boundary.

## Key Files

| File | Description |
|------|-------------|
| `truncate.go` | `Truncate()`, `TruncateString()`, `TrimRunes()`, `splitString()` |
| `estimate.go` | `ApproxTokenCount()`, `CharsToTokens()`, `TokensToChars()`, `ApproxBytesPerToken` |