# GCache v2 性能审查报告

> **审查日期**: 2026-04-23
> **审查重点**: 性能问题现状、性能回归、新发现问题、优化建议

---

## 执行摘要

| 指标 | 状态 | 说明 |
|------|------|------|
| **热路径性能** | ✅ 优秀 | Get/Set 零分配，纳秒级延迟 |
| **P2 问题** | 4 个 | Len/Purge 串行锁 + 两处 GC 压力 |
| **P3 问题** | 3 个（新发现） | 时间轮 map 开销、边界检查开销 |
| **性能回归** | 无 | P0 修复未引入可测量回归 |

**基准测试结果**（arm64, 8核）:
```
Set:           71 ns/op,  0 allocs
GetHit:        28 ns/op,  0 allocs
GetMiss:       12 ns/op,  0 allocs
SetParallel:   42 ns/op,  0 allocs
GetParallel:   45 ns/op,  0 allocs
MixedParallel: 30 ns/op,  0 allocs
```

---

## 1. 已知问题审查（P2/P3）

### 1.1 Len() 串行锁获取 [P2]

**位置**: `sharded_cache.go:236-245`

```go
func (c *shardedCache[K, V]) Len() int {
    total := 0
    for i := range c.shards {
        shard := &c.shards[i]
        shard.mu.Lock()        // 顺序获取每个分片锁
        total += int(shard.list.len())
        shard.mu.Unlock()
    }
    return total
}
```

**问题分析**:
- O(N) 锁获取复杂度，N 为分片数（默认 16-1024）
- 每次调用阻塞所有写操作，延迟约 N × (锁竞争 + 计数)
- 1024 分片场景：~100μs 量级延迟
- 监控/统计高频调用场景下成为瓶颈

**优化方案**:

方案 A：原子计数器（推荐）
```go
type shardedCache[K comparable, V any] struct {
    shards     []cacheShard[K, V]
    count      atomic.Int64  // 新增
    // ...
}

// Set 路径：成功添加时
c.count.Add(1)

// Remove 路径：成功删除时
c.count.Add(-1)

// Len 路径
func (c *shardedCache[K, V]) Len() int {
    return int(c.count.Load())
}
```

**权衡**:
- ✅ O(1) 复杂度，无锁竞争
- ⚠️ 最终一致性快照（与实际分片状态可能短暂不一致）
- ⚠️ 需要修改 Set/Remove/Purge 所有修改 count 的路径

**预估工作量**: 2 小时

---

### 1.2 Purge() 串行锁获取 [P2]

**位置**: `sharded_cache.go:210-234`

```go
func (c *shardedCache[K, V]) Purge() {
    for i := range c.shards {
        shard := &c.shards[i]
        var evicted []evictRecord[K, V]

        shard.mu.Lock()
        // ... 遍历删除
        shard.mu.Unlock()

        for _, rec := range evicted {
            c.fireOnEvict(rec)
        }
    }
}
```

**问题分析**:
- 串行处理分片，总时间 = Σ(分片处理时间)
- 大缓存（百万级条目）清空可能耗时秒级
- 清空期间阻塞所有读写操作

**优化方案**:

方案 A：并行处理（推荐）
```go
func (c *shardedCache[K, V]) Purge() {
    var wg sync.WaitGroup
    var mu sync.Mutex
    var allEvicted []evictRecord[K, V]

    for i := range c.shards {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            shard := &c.shards[idx]
            var evicted []evictRecord[K, V]

            shard.mu.Lock()
            // ... 遍历删除
            shard.mu.Unlock()

            if len(evicted) > 0 {
                mu.Lock()
                allEvicted = append(allEvicted, evicted...)
                mu.Unlock()
            }
        }(i)
    }
    wg.Wait()

    for _, rec := range allEvicted {
        c.fireOnEvict(rec)
    }
}
```

**权衡**:
- ✅ 并行度 = min(分片数, GOMAXPROCS)
- ⚠️ 回调顺序不确定（原本也不保证）
- ⚠️ 需要同步收集 evicted 记录

**预估工作量**: 3 小时

---

### 1.3 cleanExpired 热路径内存分配 [P3]

**位置**: `sharded_cache.go:319-359`

```go
func (c *shardedCache[K, V]) cleanExpired(nowNano int64) {
    for i := range c.shards {
        shard := &c.shards[i]
        if shard.wheel == nil {
            continue
        }

        var evicted []evictRecord[K, V]  // 每次分配新 slice

        shard.mu.Lock()
        shard.wheel.popExpiredInto(nowNano, &shard.expiredBuf)
        // ...
```

**问题分析**:
- 每次 cleanLoop tick 都分配新的 `evicted` slice
- 高频过期场景（短 TTL + 高吞吐）产生 GC 压力
- `popExpiredInto` 通过传入 buffer 复用消除 tick 级分配

**优化方案**:

方案 A：分片级 buffer 复用
```go
type cacheShard[K comparable, V any] struct {
    mu         sync.Mutex
    items      map[K]uint32
    list       *ringList[K, V]
    wheel      *timeWheel
    maxEntries uint32
    expiredBuf []uint32       // 新增：过期索引 buffer
    evictedBuf []evictRecord[K, V]  // 新增：淘汰记录 buffer
}

func (c *shardedCache[K, V]) cleanExpired(nowNano int64) {
    for i := range c.shards {
        shard := &c.shards[i]
        if shard.wheel == nil {
            continue
        }

        shard.mu.Lock()
        shard.expiredBuf = shard.expiredBuf[:0]
        shard.wheel.popExpiredInto(nowNano, &shard.expiredBuf)

        shard.evictedBuf = shard.evictedBuf[:0]
        for _, idx := range shard.expiredBuf {
            // ...
            rec, ok := shard.removeIndexLocked(idx, EvictReasonExpired)
            if ok {
                shard.evictedBuf = append(shard.evictedBuf, rec)
            }
        }
        shard.mu.Unlock()

        for _, rec := range shard.evictedBuf {
            c.fireOnEvict(rec)
        }
    }
}
```

**权衡**:
- ✅ 消除 tick 级别的内存分配
- ⚠️ 增加分片结构体内存占用（通常 < 1KB）
- ⚠️ 需要同步修改 `popExpiredInto` 接口（已实施）

**预估工作量**: 1.5 小时

---

### 1.4 timeWheel.popExpiredInto 返回新 slice [P3]（已修复）

**位置**: `time_wheel.go:104-150`

优化前：`popExpired` 每次调用分配新 slice
```go
func (tw *timeWheel) popExpired(nowNano int64) []uint32 {
    currentSlot := nowNano / tw.resolution
    var expired []uint32  // 每次分配新 slice
    // ...
    return expired
}
```

**问题分析**:
- 每次调用分配新 slice
- 与 cleanExpired 配合使用，叠加 GC 压力

**优化方案**:

方案 A：传入 buffer 复用
```go
func (tw *timeWheel) popExpiredInto(nowNano int64, buf *[]uint32) {
    *buf = (*buf)[:0]  // 重置长度，复用容量

    currentSlot := nowNano / tw.resolution
    start := tw.lastProcessedSlot + 1
    if start > currentSlot {
        tw.lastProcessedSlot = currentSlot - 1
        return
    }

    for slotKey := start; slotKey <= currentSlot; slotKey++ {
        bucketIdx := tw.slotIndex(slotKey)
        bucket := tw.slots[bucketIdx]
        if bucket == nil {
            continue
        }

        for idx := range bucket {
            mappedSlot, ok := tw.indexToSlot[idx]
            if !ok {
                delete(bucket, idx)
                continue
            }
            if mappedSlot != slotKey {
                continue
            }

            *buf = append(*buf, idx)
            delete(bucket, idx)
            delete(tw.indexToSlot, idx)
        }

        if len(bucket) == 0 {
            tw.slots[bucketIdx] = nil
        }
    }

    tw.lastProcessedSlot = currentSlot
}
```

**预估工作量**: 0.5 小时（与 1.3 配合）

---

## 2. 新发现的性能问题

### 2.1 时间轮 bucket 使用 map[uint32]struct{} [P3]

**位置**: `time_wheel.go:8`

```go
type timeWheel struct {
    slots             []map[uint32]struct{}  // 每个槽位一个 map
    indexToSlot       map[uint32]int64       // 反向索引
    // ...
}
```

**问题分析**:
- 每个 slot 是独立的 map，高频过期场景下频繁分配/释放
- `map[uint32]struct{}` 内存开销约 48 bytes/entry（Go map 元数据）
- 双重 map（slots + indexToSlot）存储开销

**优化方案**:

方案 A：单层 map + slot 标记
```go
type timeWheel struct {
    // 单层 map：idx -> expireAt
    entries     map[uint32]int64
    resolution  int64
    slotCount   int64
    lastProcessedSlot int64
}

func (tw *timeWheel) popExpiredInto(nowNano int64, buf *[]uint32) {
    *buf = (*buf)[:0]
    currentSlot := nowNano / tw.resolution

    for idx, expireAt := range tw.entries {
        expireSlot := expireAt / tw.resolution
        if expireSlot <= currentSlot {
            *buf = append(*buf, idx)
            delete(tw.entries, idx)
        }
    }
    tw.lastProcessedSlot = currentSlot
}
```

**权衡**:
- ✅ 消除 slot 级 map 分配
- ⚠️ 失去 slot 分段扫描优势（O(N) vs O(扫描槽数)）
- ⚠️ 需权衡过期精度与扫描开销

**建议**: 保持现有设计，双层 map 在 24h 窗口内提供 O(扫描槽数) 复杂度，适合生产环境。

---

### 2.2 ringList.mustValidIndex 边界检查开销 [P3]

**位置**: `ringlist.go:159-163`

```go
func (r *ringList[K, V]) mustValidIndex(idx uint32) {
    if idx >= uint32(len(r.entries)) {
        panic("gcache: ringList index out of range")
    }
}
```

**问题分析**:
- 每个 get/pushFront/remove/freeEntry 操作都调用
- 在热路径（Get/Set）增加额外条件判断
- P0 修复引入，确保索引安全性

**优化方案**:

方案 A：仅在调试模式启用
```go
// +build !release

func (r *ringList[K, V]) mustValidIndex(idx uint32) {
    if idx >= uint32(len(r.entries)) {
        panic("gcache: ringList index out of range")
    }
}

// +build release

func (r *ringList[K, V]) mustValidIndex(idx uint32) {
    // 生产环境：空实现
}
```

**权衡**:
- ✅ 消除热路径边界检查开销
- ⚠️ 生产环境失去防御性保护
- ⚠️ 需要构建标签管理

**建议**: 保持现状。边界检查是必要的安全措施，开销可忽略（~1-2ns）。

---

### 2.3 Purge() 内 evicted slice 累加模式 [P3]

**位置**: `sharded_cache.go:213-232`

```go
func (c *shardedCache[K, V]) Purge() {
    for i := range c.shards {
        shard := &c.shards[i]
        var evicted []evictRecord[K, V]

        shard.mu.Lock()
        // ...
        for _, rec := range evicted {
            c.fireOnEvict(rec)
        }
    }
}
```

**问题分析**:
- 每个分片独立分配 evicted slice
- 大分片淘汰可能触发多次扩容

**优化方案**: 与 1.2 并行化方案结合，使用预分配 buffer。

---

## 3. 性能回归检查

### 3.1 P0 修复的性能影响

| 修复项 | 代码变更 | 性能影响 | 结论 |
|--------|----------|----------|------|
| 时间轮负索引 | `slotIndex()` 增加负数处理 | +1 条件判断，<1ns | ✅ 可忽略 |
| ringList 边界检查 | `mustValidIndex()` 增加 | 每操作 +1 条件判断，~1-2ns | ✅ 可忽略 |
| 容量淘汰原因修正 | `evictTailLocked()` 增加 isExpired 检查 | 淘汰路径 +1 条件判断 | ✅ 可忽略 |
| alloc 失败处理 | 返回 `ErrCapacityExceeded` | 无额外开销 | ✅ 无影响 |

**结论**: P0 修复未引入可测量的性能回归。

---

## 4. 优化建议优先级

### 高优先级（P2）

| 问题 | 影响 | 收益 | 工作量 | 建议 |
|------|------|------|--------|------|
| Len() 串行锁 | 监控瓶颈 | O(N) → O(1) | 2h | ✅ 推荐实施 |
| Purge() 串行锁 | 大缓存清空阻塞 | 并行加速 | 3h | ✅ 推荐实施 |

### 中优先级（P3）

| 问题 | 影响 | 收益 | 工作量 | 建议 |
|------|------|------|--------|------|
| cleanExpired buffer 分配 | GC 压力 | 消除 tick 级分配 | 2h | 视场景评估 |
| popExpiredInto buffer 复用 | GC 压力 | 配合上述 | 0.5h | 视场景评估 |

### 低优先级（P4）

| 问题 | 影响 | 收益 | 工作量 | 建议 |
|------|------|------|--------|------|
| 时间轮双层 map | 内存开销 | 减少元数据 | 4h+ | 不推荐（破坏复杂度优势）|
| 边界检查移除 | ~1-2ns/op | 极微优化 | 0.5h | 不推荐（牺牲安全性）|

---

## 5. 实施建议

### 第一阶段（可选）：Len() 原子计数器

**适用场景**:
- 高频监控调用（每秒 > 100 次）
- 分片数 > 256

**实施步骤**:
1. 在 `shardedCache` 添加 `count atomic.Int64`
2. Set 成功时 `count.Add(1)`
3. Remove/Delete 成功时 `count.Add(-1)`
4. Purge 时批量重置
5. 添加并发测试验证一致性

### 第二阶段（可选）：Purge() 并行化

**适用场景**:
- 大缓存（> 100 万条目）
- 需要快速清空/重置

**实施步骤**:
1. 使用 `sync.WaitGroup` 并行处理分片
2. 使用 `sync.Mutex` 收集 evicted 记录
3. 保持回调在锁外触发
4. 添加回归测试验证回调触发

### 第三阶段（可选）：Buffer 复用

**适用场景**:
- 短 TTL（< 1 秒）+ 高吞吐
- GC 敏感场景

**实施步骤**:
1. 在 `cacheShard` 添加 buffer 字段
2. 修改 `popExpired` 为 `popExpiredInto`（已完成）
3. cleanExpired 使用分片级 buffer
4. 添加基准测试对比 GC 压力

---

## 6. 总结

### 当前状态

**GCache v2 热路径性能已达到优秀水平**：
- Get/Set 零分配
- 纳秒级延迟
- 并行扩展性良好

### 主要问题

1. **Len()/Purge() 串行锁**：全局操作成为瓶颈
2. **cleanExpired GC 压力**：高频过期场景下有优化空间

### 建议

- **P2 问题**：根据实际使用场景评估是否需要优化
  - 监控不频繁：Len() 可保持现状
  - 不常 Purge：可保持现状
- **P3 问题**：仅在 GC 敏感场景下考虑优化

### 底线

**性能优化应遵循"先测量，后优化"原则**：
1. 确认存在实际性能瓶颈
2. 使用 pprof 确认热点
3. 权衡优化收益与实现复杂度

---

> **审查人**: Oracle (性能审查专项)
> **审查方法**: 代码静态分析 + 基准测试验证
