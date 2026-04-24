# 数据契约与接口变化

## 公共接口

v2 的 `Cache[K, V]` 采用泛型设计，与 v1 `Cache` (`interface{}` 基) 相比，接口存在多处差异：

| 方法 | v1 签名 | v2 签名 | 变化 |
|------|---------|---------|------|
| `Set` | `Set(key, value interface{}) error` | `Set(key K, value V) error` | **泛型化** |
| `SetWithExpire` | `SetWithExpire(key, value interface{}, expiration time.Duration) error` | `SetWithExpire(key K, value V, ttl time.Duration) error` | **泛型化，参数名 change** |
| `Get` | `Get(key interface{}) (interface{}, error)` | `Get(key K) (V, error)` | **泛型化** |
| `GetIFPresent` | `GetIFPresent(key interface{}) (interface{}, error)` | `GetIFPresent(key K) (V, error)` | **泛型化** |
| `Remove` | `Remove(key interface{}) bool` | `Remove(key K) bool` | **泛型化** |
| `Purge` | `Purge()` | `Purge(notify bool)` | **新增 `notify` 参数**，控制是否触发手动淘汰回调 |
| `Len` | `Len(checkExpired bool) int` | `Len() int` | **移除 `checkExpired` 参数** |
| `Close` | ❌ 不存在 | `Close()` | **v2 新增** |
| `Stats` | `statsAccessor` (嵌入 Cache 接口，提供 `HitCount()`/`MissCount()` 等) | `Stats` 结构体 + `StatsAccessor` 接口（均不嵌入 Cache） | `Stats()` 为具体实现 `*shardedCache` 的方法，非 `Cache` 接口成员；调用方需做类型断言 |
| `GetALL` | `GetALL(checkExpired bool) map[interface{}]interface{}` | ❌ 移除 | **v2 不支持** |
| `Keys` | `Keys(checkExpired bool) []interface{}` | ❌ 移除 | **v2 不支持** |
| `Has` | `Has(key interface{}) bool` | `Has(key K) bool` | **泛型化** |

> 总结：v2 泛型化所有签名，移除 `GetALL`/`Keys`，新增 `Close()`，变更 `Purge` 和 `Len` 签名。`Has` 保留并泛型化；`Stats()` 为具体实现方法，不在 `Cache` 接口中。

错误语义补充：
- `Set/SetWithExpire` 在极端容量边界（内部索引无法再分配）时返回 `ErrCapacityExceeded`。
- `Get/GetIFPresent` 继续使用 `ErrNotFound` 表示不存在或已过期。

## Builder 扩展
新增：
- `Shards(n int) *Builder[K, V]`

约束：
- `n <= 0` 时采用自动分片策略。
- 最终分片数内部归一化为 2 的幂。
- `capacity > 0` 为有限容量模式；`capacity <= 0` 为无限容量模式。

## 内部结构（仅签名级约束）
- `newShardedCache(capacity, ttl, resolution, onEvict, shardCount)`
- `shardedCache.Stats() Stats`
- `timeWheel` 使用 24h 固定窗口的环形槽数组组织到期记录，并提供：`add/remove/popExpiredInto/clear`
- `Resolution` 会在构建阶段做归一化，保证槽数量不超过内部上限，避免异常大内存分配

淘汰原因语义补充：
- 容量淘汰路径若命中“已过期尾节点”，应上报 `EvictReasonExpired`，避免误报 `EvictReasonCapacity`。
