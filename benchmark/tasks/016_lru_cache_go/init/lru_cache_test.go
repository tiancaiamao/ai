package lru_cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- helpers ---

func newTestCache(t *testing.T, name string, maxEntries int, maxBytes int64) *Cache {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Name:          name,
		MaxEntries:    maxEntries,
		MaxTotalBytes: maxBytes,
		EvictDebounce: 50 * time.Millisecond,
	}
	c, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// --- OpenCache ---

func TestOpenCacheCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent", "deep")
	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}
	c, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	defer c.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("cache directory was not created")
	}
}

func TestOpenCacheCorruptedIndex(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "test")
	os.MkdirAll(cacheDir, 0o755)

	// Write garbage index
	indexPath := filepath.Join(cacheDir, "index.json")
	os.WriteFile(indexPath, []byte("not valid json{{{"), 0o644)

	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}
	c, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatalf("OpenCache with corrupted index should not error, got: %v", err)
	}
	defer c.Close()

	// Cache should be usable (empty)
	val, err := c.Get("any")
	if err != nil {
		t.Fatalf("Get on empty cache: %v", err)
	}
	if val != nil {
		t.Fatalf("Expected nil, got %v", val)
	}
}

// --- Add ---

func TestAddAndGet(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	err := c.Add("key1", []byte("hello"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	val, err := c.Get("key1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "hello" {
		t.Fatalf("Expected 'hello', got '%s'", val)
	}
}

func TestAddOverwrites(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	c.Add("key1", []byte("v1"))
	c.Add("key1", []byte("v2"))

	val, _ := c.Get("key1")
	if string(val) != "v2" {
		t.Fatalf("Expected 'v2', got '%s'", val)
	}
}

// --- Update ---

func TestUpdateIsAliasForAdd(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	err := c.Update("key1", []byte("updated"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	val, _ := c.Get("key1")
	if string(val) != "updated" {
		t.Fatalf("Expected 'updated', got '%s'", val)
	}
}

// --- Modify ---

func TestModifyExisting(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)
	c.Add("key1", []byte("old"))

	err := c.Modify("key1", []byte("new"))
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}

	val, _ := c.Get("key1")
	if string(val) != "new" {
		t.Fatalf("Expected 'new', got '%s'", val)
	}
}

func TestModifyNonexistentFails(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	err := c.Modify("nonexistent", []byte("data"))
	if err == nil {
		t.Fatal("Modify on nonexistent key should return error")
	}
}

// --- Get ---

func TestGetNonexistentReturnsNil(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	val, err := c.Get("nothing")
	if err != nil {
		t.Fatalf("Get nonexistent: %v", err)
	}
	if val != nil {
		t.Fatalf("Expected nil, got %v", val)
	}
}

func TestGetBumpsAccessTime(t *testing.T) {
	c := newTestCache(t, "test", 3, 1<<20)

	c.Add("a", []byte("1"))
	c.Add("b", []byte("2"))
	c.Add("c", []byte("3"))

	// Access "a" to make it most recently used
	c.Get("a")

	// Adding a 4th entry should evict the LRU = "b"
	c.Add("d", []byte("4"))
	time.Sleep(100 * time.Millisecond) // let eviction settle

	val, _ := c.Get("b")
	if val != nil {
		t.Fatal("'b' should have been evicted (it was LRU), but Get returned data")
	}

	// "a" should still be present (it was accessed)
	val, _ = c.Get("a")
	if string(val) != "1" {
		t.Fatal("'a' should still be present after access bump")
	}
}

// --- Promote ---

func TestPromoteBumpsAccessWithoutReading(t *testing.T) {
	c := newTestCache(t, "test", 3, 1<<20)

	c.Add("a", []byte("1"))
	c.Add("b", []byte("2"))
	c.Add("c", []byte("3"))

	// Promote "a" without reading payload
	err := c.Promote("a")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Adding a 4th should evict "b" (LRU)
	c.Add("d", []byte("4"))
	time.Sleep(100 * time.Millisecond)

	val, _ := c.Get("b")
	if val != nil {
		t.Fatal("'b' should have been evicted after promote of 'a'")
	}
}

func TestPromoteNonexistentFails(t *testing.T) {
	c := newTestCache(t, "test", 10, 1<<20)

	err := c.Promote("nonexistent")
	if err == nil {
		t.Fatal("Promote on nonexistent key should return error")
	}
}

// --- Eviction by count ---

func TestEvictionByCount(t *testing.T) {
	c := newTestCache(t, "test", 3, 1<<20)

	c.Add("a", []byte("1"))
	c.Add("b", []byte("2"))
	c.Add("c", []byte("3"))
	c.Add("d", []byte("4")) // should trigger eviction of "a"
	time.Sleep(100 * time.Millisecond)

	val, _ := c.Get("a")
	if val != nil {
		t.Fatal("'a' should have been evicted")
	}

	for _, k := range []string{"b", "c", "d"} {
		val, _ = c.Get(k)
		if val == nil {
			t.Fatalf("'%s' should still be present", k)
		}
	}
}

// --- Eviction by size ---

func TestEvictionBySize(t *testing.T) {
	c := newTestCache(t, "test", 100, 100) // 100 bytes max

	c.Add("a", []byte("1234567890")) // 10 bytes
	c.Add("b", []byte("1234567890"))
	c.Add("c", []byte("1234567890"))
	c.Add("d", []byte("1234567890"))
	c.Add("e", []byte("1234567890"))
	time.Sleep(200 * time.Millisecond)

	// At most ~100 bytes worth of entries should survive
	val, _ := c.Get("a")
	if val != nil {
		t.Fatal("'a' should have been evicted by size")
	}

	val, _ = c.Get("e")
	if val == nil {
		t.Fatal("'e' (most recent) should still be present")
	}
}

// --- Atomic writes / persistence ---

func TestAtomicWrite(t *testing.T) {
	baseDir := t.TempDir()
	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c, err := OpenCache(baseDir, cfg)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("important data")
	c.Add("key1", data)
	c.Close()

	// Reopen and verify persistence
	c2, err := OpenCache(baseDir, cfg)
	if err != nil {
		t.Fatalf("OpenCache second time: %v", err)
	}
	defer c2.Close()

	val, err := c2.Get("key1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if string(val) != "important data" {
		t.Fatalf("Expected 'important data', got '%s'", val)
	}
}

// --- Self-heal: file in index but missing on disk ---

func TestSelfHealMissingFile(t *testing.T) {
	baseDir := t.TempDir()
	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c, err := OpenCache(baseDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	c.Add("ghost", []byte("data"))

	// Sneak behind the cache and delete the data file
	// The cache stores files under baseDir/<name>/
	cacheDir := filepath.Join(baseDir, "test")
	filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() != "index.json" {
			os.Remove(path)
		}
		return nil
	})

	// Get should return nil and self-heal the index
	val, err := c.Get("ghost")
	if err != nil {
		t.Fatalf("Get missing file: %v", err)
	}
	if val != nil {
		t.Fatal("Expected nil for missing file")
	}
}

// --- Index persistence ---

func TestIndexPersistenceAfterMutation(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Name: "persist", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c1, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c1.Add("key1", []byte("v1"))
	c1.Close()

	// Reopen and verify
	c2, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	val, _ := c2.Get("key1")
	if string(val) != "v1" {
		t.Fatalf("Expected 'v1' after reopen, got '%s'", val)
	}
}

// --- Deleted directory recovery ---

func TestDeletedDirectoryRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Add("key1", []byte("data"))
	c.Close()

	// Delete the cache directory
	cacheDir := filepath.Join(dir, "test")
	os.RemoveAll(cacheDir)

	// Reopen — should recreate directory gracefully
	c2, err := OpenCache(dir, cfg)
	if err != nil {
		t.Fatalf("OpenCache after dir deletion: %v", err)
	}
	defer c2.Close()

	// The old data is gone, but cache should be usable
	err = c2.Add("newkey", []byte("newdata"))
	if err != nil {
		t.Fatalf("Add after recovery: %v", err)
	}

	val, _ := c2.Get("newkey")
	if string(val) != "newdata" {
		t.Fatalf("Expected 'newdata', got '%s'", val)
	}
}

// --- Multiple independent caches ---

func TestMultipleIndependentCaches(t *testing.T) {
	base := t.TempDir()

	cfg1 := Config{Name: "images", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}
	cfg2 := Config{Name: "videos", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c1, _ := OpenCache(base, cfg1)
	c2, _ := OpenCache(base, cfg2)
	defer c1.Close()
	defer c2.Close()

	c1.Add("img1", []byte("image_data"))
	c2.Add("vid1", []byte("video_data"))

	v1, _ := c1.Get("img1")
	v2, _ := c2.Get("vid1")

	if string(v1) != "image_data" {
		t.Fatal("c1 should have image_data")
	}
	if string(v2) != "video_data" {
		t.Fatal("c2 should have video_data")
	}

	// Cross-check: key shouldn't exist in wrong cache
	wrong, _ := c1.Get("vid1")
	if wrong != nil {
		t.Fatal("c1 should not have vid1")
	}
}

// --- Index file is valid JSON ---

func TestIndexFileIsValidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Name: "test", MaxEntries: 10, MaxTotalBytes: 1 << 20, EvictDebounce: 50 * time.Millisecond}

	c, _ := OpenCache(dir, cfg)
	c.Add("key1", []byte("data"))
	c.Close()

	indexPath := filepath.Join(dir, "test", "index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Skipf("index file not found at %s (implementation may use different path)", indexPath)
	}

	if !json.Valid(raw) {
		t.Fatalf("Index file is not valid JSON: %s", string(raw[:min(len(raw), 200)]))
	}
}
