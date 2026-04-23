# GCache 项目文档索引

## 项目概述

GCache 是一个高性能 Go 缓存库，当前同时维护：

- `v1`：多策略缓存（LRU/LFU/ARC/Simple），以兼容为主。
- `v2`：泛型 + 分片并发 + LRU + TTL 的高性能实现，处于“可用并持续优化”阶段。

## 版本说明

| 版本 | 状态 | 说明 |
|------|------|------|
| v1 | 维护模式 | 支持 LRU/LFU/ARC/Simple，使用 `interface{}` |
| **v2** | 可用（持续优化） | 泛型实现，支持分片并发、LRU、TTL、淘汰原因回调 |

## 文档路由

| 文档 | 说明 |
|------|------|
| [v2 设计文档](./v2-design.md) | v2 的架构设计、过期语义、API 约束（面向使用与评审） |
| [v2 实现索引](../v2/docs/INDEX.md) | v2 子项目 DDD 索引，包含实现变更与子文档路由（面向开发） |
| [v2 代码审查报告](../v2/docs/CODE_REVIEW.md) | v2 全面代码审查（Panic/死锁/业务逻辑/性能） |

## 快速开始

### v2 版本（推荐）

```go
import "github.com/puper/gcache/v2"

cache := gcache.New[string, *User](10000).
    TTL(10 * time.Minute).
    Resolution(time.Second). // 后台清理粒度（可选）
    OnEvict(func(key string, value *User, reason gcache.EvictReason) {
        log.Printf("缓存淘汰: %s, 原因: %v", key, reason)
    }).
    Build()

_ = cache.Set("user:123", &User{Name: "Alice"})
user, err := cache.Get("user:123")
_ = user
_ = err
```

说明：

- `Resolution(...)` 控制 TTL 后台清理 tick 粒度。
- `NoClean()` 可关闭后台清理，仅在访问路径惰性回收过期项。
- v2 当前无 `CleanInterval(...)` API。

### v1 版本

```go
import "github.com/puper/gcache"

gc := gcache.New(20).LRU().Build()
gc.Set("key", "value")
```

## 核心特性

### v2 版本

- 泛型支持：类型安全，无运行时断言
- 分片并发：默认按 `GOMAXPROCS` 自动推导分片数，支持 `Shards(n)` 覆盖
- LRU + TTL：惰性过期 + 可选后台清理（时间轮）
- 回调语义清晰：区分容量淘汰、过期淘汰、手动删除

### v1 版本

- 多种淘汰策略：LRU、LFU、ARC、Simple
- 自动加载、过期机制
- 事件回调

## 最近更新

- 2026-04-22：修正 v2 设计文档与实现不一致问题（含后台清理策略描述）。
- 2026-04-22：补充 v2 子项目文档索引入口，区分“设计文档”与“实现文档”。
- 2026-04-23：完成 v2 全面代码审查（Panic/死锁/业务逻辑/性能），发现 5 个 P0 级别问题需修复，详见 [v2 代码审查报告](../v2/docs/CODE_REVIEW.md)。
