# pkg/modelselect

Model query selection with exact and prefix matching.

## Overview

Provides generic model selection by query string. Matches against a collection of models using exact match first, then prefix match. Returns an error for ambiguous queries.

## API

### SortByModelKey

```go
func SortByModelKey[T any](items []T, extract KeyExtractor[T])
```

Sorts models by provider, ID, then name (case-insensitive).

### SelectByQuery

```go
func SelectByQuery[T any](items []T, query string, extract KeyExtractor[T]) (T, error)
```

Resolves a model query:
1. Exact match on ID
2. Exact match on name
3. Prefix match on ID
4. Prefix match on name
5. Returns `ErrNotFound` if no match, `ErrAmbiguous` if multiple matches

### KeyExtractor

```go
type KeyExtractor[T any] func(item T) Keys

type Keys struct {
    Provider string
    ID       string
    Name     string
}
```

Extractor function that maps items to normalized identity fields.

## Errors

```go
var ErrNotFound  = errors.New("model not found")
var ErrAmbiguous = errors.New("model selector is ambiguous")
```

## Key Files

| File | Description |
|------|-------------|
| `modelselect.go` | `SelectByQuery`, `SortByModelKey`, `KeyExtractor` |