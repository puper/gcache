// Package gcache v2 - 高性能泛型缓存库
//
// 特性：
//   - 泛型支持 (Go 1.18+)
//   - LRU 淘汰策略
//   - TTL 过期机制
//   - 分片锁并发安全
//   - 区分淘汰原因的回调
//
// 示例:
//
//	cache := gcache.New[string, *User](10000).
//	    TTL(10 * time.Minute).
//	    OnEvict(func(key string, value *User, reason gcache.EvictReason) {
//	        log.Printf("淘汰: %s, 原因: %v", key, reason)
//	    }).
//	    Build()
//
//	cache.Set("user:123", &User{Name: "Alice"})
//	user, err := cache.Get("user:123")
package gcache

import (
	"errors"
	"time"
)

// ErrNotFound 键不存在错误
var ErrNotFound = errors.New("key not found")

// EvictReason 淘汰原因
type EvictReason int

const (
	// EvictReasonCapacity 容量淘汰（缓存满时淘汰最久未使用）
	EvictReasonCapacity EvictReason = iota
	// EvictReasonExpired 过期淘汰（TTL 到期）
	EvictReasonExpired
	// EvictReasonManual 手动删除（Remove/Purge）
	EvictReasonManual
)

// String 返回淘汰原因的字符串表示
func (r EvictReason) String() string {
	switch r {
	case EvictReasonCapacity:
		return "capacity"
	case EvictReasonExpired:
		return "expired"
	case EvictReasonManual:
		return "manual"
	default:
		return "unknown"
	}
}

// EvictCallback 淘汰回调函数
type EvictCallback[K comparable, V any] func(key K, value V, reason EvictReason)

// Cache 缓存接口
type Cache[K comparable, V any] interface {
	// Set 设置键值对，使用默认 TTL
	Set(key K, value V) error

	// SetWithExpire 设置键值对并指定过期时间
	SetWithExpire(key K, value V, ttl time.Duration) error

	// Get 获取值，不存在返回 ErrNotFound
	// 如果设置了 TTL 且已过期，返回 ErrNotFound 并触发过期淘汰回调
	Get(key K) (V, error)

	// GetIFPresent 仅在缓存中存在时返回值
	// 不会触发加载（与 v1 不同，v2 不内置 Loader）
	GetIFPresent(key K) (V, error)

	// Remove 删除指定键，返回是否删除成功
	Remove(key K) bool

	// Purge 清空缓存，触发所有条目的手动淘汰回调
	Purge()

	// Len 返回缓存条目数
	Len() int

	// Has 检查键是否存在（且未过期）
	Has(key K) bool

	// Close 关闭缓存，停止后台清理 goroutine
	Close()
}

// Stats 缓存统计信息
type Stats struct {
	Hits   uint64 // 命中次数
	Misses uint64 // 未命中次数
}

// StatsAccessor 统计信息访问接口
type StatsAccessor interface {
	HitCount() uint64
	MissCount() uint64
	LookupCount() uint64
	HitRate() float64
}
