# Task: Implement LRU File Cache (Go)

## Description

Implement a disk-backed LRU cache in Go based on the specification below. You are given a test file `lru_cache_test.go` that your implementation must pass.

## Requirements

- Disk-backed LRU cache supporting multiple independent named caches
- LRU order determined by last-accessed time
- Eviction runs asynchronously and must not block reads or writes
- All insertions use atomic writes (write to temp file, then rename)
- The index (LRU metadata) is persisted to disk and survives process restarts
- Fast start: cache must be usable immediately after init with no blocking I/O on the hot path

## Configuration (per cache)

- Name (used as the on-disk directory namespace)
- Max entry count
- Max total byte size
- Eviction debounce interval (how long to wait before persisting index after reads)

## API

```go
type Cache struct { ... }

type Config struct {
    Name              string
    MaxEntries        int
    MaxTotalBytes     int64
    EvictDebounce     time.Duration
}

func OpenCache(baseDir string, cfg Config) (*Cache, error)
func (c *Cache) Add(key string, data []byte) error       // Insert or replace
func (c *Cache) Update(key string, data []byte) error     // Alias for Add
func (c *Cache) Modify(key string, data []byte) error     // Replace only; error if absent
func (c *Cache) Get(key string) ([]byte, error)           // Return payload, bump access time
func (c *Cache) Promote(key string) error                 // Bump access time without reading payload
func (c *Cache) Close() error                             // Persist index and release resources
```

## Constraints

- `Modify` must return an error if the key does not exist; `Add` and `Update` must not
- `Get` and `Promote` both count as an access and must update LRU order
- Index persistence triggered by `Get`/`Promote` should be debounced
- Index persistence triggered by mutations (`Add`, `Modify`, eviction) must be immediate
- A cache directory deleted externally must be recreated on next write
- A corrupted index on init must degrade gracefully to an empty cache, not a crash
- A file present in the index but missing on disk at `Get` time must return nil and self-heal the index

## Files

- `lru_cache.go` — your implementation (create this file)
- `lru_cache_test.go` — the test suite (do NOT modify)
- `go.mod` — already provided

## Success Criteria

- `go test -v -count=1 ./...` passes all tests
- `go vet ./...` shows no issues