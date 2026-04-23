# 数据契约与接口变化

## 公共接口
`Cache[K, V]` 对外接口保持不变：
- `Set`
- `SetWithExpire`
- `Get`
- `GetIFPresent`
- `Remove`
- `Purge`
- `Len`
- `Has`
- `Close`

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
- `timeWheel` 使用 24h 固定窗口的环形槽数组组织到期记录，并提供：`add/remove/popExpired/clear`
- `Resolution` 会在构建阶段做归一化，保证槽数量不超过内部上限，避免异常大内存分配

淘汰原因语义补充：
- 容量淘汰路径若命中“已过期尾节点”，应上报 `EvictReasonExpired`，避免误报 `EvictReasonCapacity`。
