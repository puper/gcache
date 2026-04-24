# GCache v2 设计文档（实现对齐版）

> 更新时间：2026-04-22
> 说明：本文档基于 `v2/` 当前代码实现整理，重点修正了后台清理策略与 API 描述偏差。

## 1. 设计目标

### 1.1 性能目标

| 指标 | 目标 |
|------|------|
| 并发读写吞吐 | 通过分片锁提升多核扩展性 |
| 单次 Get/Set 复杂度 | 平均 O(1) |
| TTL 清理复杂度 | 近似 O(扫描槽数 + 过期候选数) |
| 运行时开销 | 避免全量扫描，降低清理抖动 |

### 1.2 功能目标

- LRU 淘汰策略
- TTL 过期机制（惰性过期 + 可选后台清理）
- 区分淘汰原因的回调（capacity / expired / manual）
- 泛型支持（Go 1.18+）
- 分片锁并发安全

## 2. 架构设计

### 2.1 分片策略

v2 不使用固定 256 分片，分片数由 Builder 参数和运行环境共同决定：

1. 显式配置：`Shards(n)`（最终归一化为 2 的幂）
2. 自动配置：`GOMAXPROCS * 4`，并约束在 `[16, 1024]`
3. 有限容量模式下，分片数不会超过 `capacity`

分片路由使用 64 位哈希和位掩码：

```go
idx := uint32(hash64(key) & shardMask)
```

### 2.2 数据结构

每个分片独立维护：

- `items map[K]uint32`：key 到条目索引
- `ringList[K, V]`：LRU 双向链表（数组 + 索引）
- `timeWheel`（可选）：TTL 后台清理结构
- `sync.Mutex`：分片互斥锁

容量语义：

- `capacity > 0`：有限容量，达到上限触发 LRU 容量淘汰
- `capacity <= 0`：无限容量，不做容量淘汰，底层按需扩容

### 2.3 过期策略（已修正）

采用“惰性过期 + 时间轮后台清理”组合，不是“定时扫描 LRU 尾部”。

1. 惰性过期（读路径）
- `Get/Has` 会检查 `expireAt`
- 发现过期即删除并触发 `EvictReasonExpired`

2. 后台清理（可选）
- 启动条件：`ttl > 0 && !NoClean() && resolution > 0`
- 调度方式：`ticker(resolution)` 驱动 `cleanLoop`
- 清理对象：每个分片的 `timeWheel.popExpiredInto(nowNano, &shard.expiredBuf)` 写入的候选索引
- 删除前校验：当前 `items` 映射和 `expireAt` 双重校验，避免误删旧索引

3. 长 TTL 处理
- 时间轮固定 24h 窗口
- 超过窗口的条目会在扫描时按真实 `expireAt` 自动续挂，直到真正到期

4. 精度语义
- TTL 为近似调度，允许在 `Resolution` 粒度内延迟过期

## 3. API 设计

### 3.1 Cache 接口

```go
type Cache[K comparable, V any] interface {
    Set(key K, value V) error
    SetWithExpire(key K, value V, ttl time.Duration) error
    Get(key K) (V, error)
    GetIFPresent(key K) (V, error)
    Remove(key K) bool
    // notify=true 触发每个条目的手动淘汰回调；notify=false 直接清空（不触发回调）。
    Purge(notify bool)
    Len() int
    Has(key K) bool
    Close()
}
```

### 3.2 Builder 配置

当前实现支持：

- `TTL(ttl time.Duration)`
- `Resolution(res time.Duration)`
- `Shards(n int)`
- `NoClean()`
- `OnEvict(cb)`

注意：v2 当前实现没有 `CleanInterval(...)` API，清理间隔由 `Resolution(...)` 指定。

示例：

```go
cache := gcache.New[string, *User](10000).
    TTL(10 * time.Minute).
    Resolution(time.Second).
    OnEvict(func(key string, value *User, reason gcache.EvictReason) {
        // 处理淘汰回调
    }).
    Build()
```

## 4. 核心算法

### 4.1 Set / SetWithExpire

- 命中已存在 key：更新值、刷新 `expireAt`、移动到 LRU 头部
- 新 key 写入：
  - 有限容量且分片已满：先淘汰 LRU 尾部
  - 分配新条目，挂到 LRU 头部
  - 若设置了 TTL，加入分片时间轮

### 4.2 Get / Has

- 先查 `items`
- 不存在：返回 miss
- 存在但过期：删除并回调 `expired`
- 存在且有效：Get 会提升到 LRU 头部

### 4.3 后台清理循环

```go
for range ticker(resolution) {
    for each shard {
        shard.wheel.popExpiredInto(nowNano, &shard.expiredBuf)
        for idx in shard.expiredBuf {
            // 校验索引有效 + 当前映射一致 + 真实到期
            // 未真实到期（长 TTL 提前触发）则续挂
            // 已到期则删除并记录回调
        }
    }
}
```

## 5. 并发与稳定性

### 5.1 锁模型

- 每分片一个 `sync.Mutex`
- 当前实现未使用 `RWMutex`

### 5.2 回调策略

- 回调统一在锁外触发，降低锁竞争和重入风险
- `onEvict` 做 panic 隔离（`recover`），避免用户回调故障影响缓存主流程

### 5.3 哈希策略

- `string`：`maphash.String`
- 常见数值：`mix64`
- 复杂可比较类型：`maphash.Comparable`

## 6. 语义说明

- `ttl <= 0`：视为不过期
- 未开启后台清理时，过期回收主要发生在读路径（`Get/Has`）
- `GetIFPresent` 当前与 `Get` 语义一致
- `Set/SetWithExpire` 当前实现返回值始终为 `nil`（保留 error 签名用于兼容扩展）

## 7. 当前状态

v2 核心功能（分片、LRU、TTL、回调、基础测试）已可用，当前工作重点是：

- 继续完善文档与实现的一致性
- 增强语义边界测试（尤其是 TTL 与容量组合场景）
- 持续做 benchmark 回归与热点分析
