# Resume 惰性加载设计

## 目标

让 resume 操作更快、更轻量，尤其是长会话场景。

## 当前问题

```go
// pkg/session/session.go - LoadSession()
func LoadSession(filePath string) (*Session, error) {
    // 读取整个文件
    data, err := os.ReadFile(filePath)
    
    // 解析所有 entries
    for _, line := range lines {
        entry, err := decodeSessionEntry(line)
        sess.addEntry(entry)  // 全部加载到内存
    }
}
```

问题：即使有 compaction entry，仍然加载全部历史。

## 设计方案

### 方案 A: 惰性加载 (推荐)

**核心思想**: 只加载 compaction entry + 最近 N 条消息

```go
type LoadOptions struct {
    // 最多加载多少条消息 (0 = 全部)
    MaxMessages int
    // 是否包含压缩摘要
    IncludeSummary bool
}

func LoadSessionLazy(filePath string, opts LoadOptions) (*Session, error) {
    // 1. 读取 header
    // 2. 从后往前扫描，找到最近的 compaction entry
    // 3. 只加载 compaction entry + 最近 N 条消息
    // 4. 跳过已被压缩的旧消息
}
```

**优点**:
- 向后兼容（可选参数）
- 大幅减少内存占用
- Resume 速度提升明显

**缺点**:
- 实现稍复杂
- 需要处理边界情况

### 方案 B: 文件指针

**核心思想**: 在 session header 记录"恢复点"文件偏移量

```go
type SessionHeader struct {
    // ... existing fields ...
    ResumeOffset int64 `json:"resumeOffset,omitempty"` // 恢复点文件偏移量
}
```

**优点**:
- 可以直接 fseek 到恢复点
- 最快的 resume

**缺点**:
- 需要维护偏移量一致性
- 文件修改后偏移量可能失效

### 方案 C: 混合方案 (推荐实施)

结合 A 和 B 的优点：

1. **优先使用 ResumeOffset** - 如果 header 有记录，直接跳到偏移量
2. **回退到扫描** - 如果偏移量无效，从后往前扫描
3. **渐进式加载** - 先加载最近 N 条，按需加载更多

## 详细设计

### 1. 扩展 SessionHeader

```go
type SessionHeader struct {
    Type          string `json:"type"`
    Version       int    `json:"version"`
    ID            string `json:"id"`
    Timestamp     string `json:"timestamp"`
    Cwd           string `json:"cwd"`
    ParentSession string `json:"parentSession,omitempty"`
    
    // 新增字段
    LastCompactionID string `json:"lastCompactionId,omitempty"` // 最近一次压缩 entry ID
    ResumeOffset     int64  `json:"resumeOffset,omitempty"`     // 恢复点文件偏移量
}
```

### 2. LoadOptions

```go
type LoadOptions struct {
    // 最多加载多少条消息 (0 = 全部，-1 = 自动根据 compaction)
    MaxMessages int
    // 是否包含压缩摘要作为历史
    IncludeSummary bool
    // 是否使用惰性加载（默认 true）
    Lazy bool
}

// DefaultLoadOptions 返回默认加载选项
func DefaultLoadOptions() LoadOptions {
    return LoadOptions{
        MaxMessages:    0,    // 自动
        IncludeSummary: true, // 包含摘要
        Lazy:           true, // 惰性加载
    }
}
```

### 3. LoadSessionLazy 实现

```go
func LoadSessionLazy(filePath string, opts LoadOptions) (*Session, error) {
    f, err := os.Open(filePath)
    defer f.Close()
    
    // 1. 读取 header
    header, err := readHeader(f)
    
    // 2. 如果有 ResumeOffset，直接跳到该位置
    if header.ResumeOffset > 0 {
        f.Seek(header.ResumeOffset, io.SeekStart)
        return loadFromPosition(f, header, opts)
    }
    
    // 3. 否则从后往前扫描，找 compaction entry
    return loadFromEnd(f, header, opts)
}

func loadFromEnd(f *os.File, header SessionHeader, opts LoadOptions) (*Session, error) {
    // 从文件末尾往前读
    // 找到最近的 compaction entry
    // 只加载 compaction + 最近 N 条消息
    
    stat, _ := f.Stat()
    size := stat.Size()
    
    // 使用反向读取器
    scanner := newReverseScanner(f, size)
    
    var compactionEntry *SessionEntry
    var recentEntries []*SessionEntry
    messageCount := 0
    
    for scanner.Scan() {
        line := scanner.Bytes()
        entry, err := decodeSessionEntry(line)
        if err != nil {
            continue
        }
        
        // 找到 compaction entry
        if entry.Type == EntryTypeCompaction {
            compactionEntry = entry
            break
        }
        
        // 收集最近的消息
        if entry.Type == EntryTypeMessage {
            recentEntries = append([]*SessionEntry{entry}, recentEntries...)
            messageCount++
            
            if opts.MaxMessages > 0 && messageCount >= opts.MaxMessages {
                break
            }
        }
    }
    
    // 构建 session
    sess := &Session{
        header: header,
        entries: recentEntries,
        // ...
    }
    
    // 添加 compaction entry 作为第一条
    if compactionEntry != nil && opts.IncludeSummary {
        sess.entries = append([]*SessionEntry{compactionEntry}, sess.entries...)
    }
    
    return sess, nil
}
```

### 4. 压缩时更新 ResumeOffset

```go
func (s *Session) Compact(compactor *compact.Compactor) (*CompactionResult, error) {
    // ... existing compaction logic ...
    
    // 创建 compaction entry
    entry := &SessionEntry{...}
    s.addEntry(entry)
    
    // 更新 header
    s.header.LastCompactionID = entry.ID
    
    // 记录当前文件位置作为恢复点
    if s.persist {
        offset, _ := s.file.Seek(0, io.SeekCurrent)
        s.header.ResumeOffset = offset
    }
    
    // 持久化
    s.persistEntry(entry)
    s.persistHeader()
}
```

### 5. 按需加载更多历史

```go
// LoadMoreHistory 加载更多历史消息（从文件）
func (s *Session) LoadMoreHistory(ctx context.Context) error {
    if s.filePath == "" {
        return errors.New("no file path")
    }
    
    // 找到当前最早的消息 ID
    if len(s.entries) == 0 {
        return nil
    }
    firstID := s.entries[0].ID
    
    // 从文件加载更早的 entries
    f, err := os.Open(s.filePath)
    defer f.Close()
    
    // 扫描找到 firstID 之前的消息
    // ...
    
    return nil
}
```

## 向后兼容性

1. **新字段使用 `omitempty`** - 旧文件正常读取
2. **默认行为不变** - `LoadSession()` 保持原逻辑
3. **新 API 可选** - `LoadSessionLazy()` 作为新入口
4. **渐进式迁移** - RPC/Win 模式逐步切换

## 性能预期

| 场景 | 当前 | 优化后 |
|------|------|--------|
| 100 条消息 resume | ~100ms | ~20ms |
| 500 条消息 resume | ~500ms | ~50ms |
| 1000 条消息 resume | ~1s | ~80ms |
| 内存占用 (500 条) | ~10MB | ~2MB |

## 实施步骤

1. **Phase 1**: 扩展 SessionHeader，添加 LoadOptions
2. **Phase 2**: 实现 LoadSessionLazy 和反向读取
3. **Phase 3**: 集成到 Compact，更新 ResumeOffset
4. **Phase 4**: 修改 RPC/Win 模式使用惰性加载
5. **Phase 5**: 测试和性能验证

## 风险

| 风险 | 缓解措施 |
|------|----------|
| 反向读取复杂 | 使用 bufio + 缓冲区 |
| 边界情况 | 充分的单元测试 |
| 向后兼容 | omitempty + 渐进迁移 |