package gcache

import (
	"runtime"
	"time"
)

const defaultResolution = time.Second

type Builder[K comparable, V any] struct {
	capacity   int
	shards     int
	ttl        time.Duration
	resolution time.Duration
	onEvict    EvictCallback[K, V]
	noClean    bool
}

// New 创建缓存构建器：
// - capacity > 0：有限容量模式（达到上限后触发 LRU 容量淘汰）
// - capacity <= 0：无限容量模式（不触发容量淘汰，按需扩容）
func New[K comparable, V any](capacity int) *Builder[K, V] {
	return &Builder[K, V]{
		capacity:   capacity,
		resolution: defaultResolution,
	}
}

func (b *Builder[K, V]) TTL(ttl time.Duration) *Builder[K, V] {
	b.ttl = ttl
	return b
}

func (b *Builder[K, V]) Resolution(res time.Duration) *Builder[K, V] {
	b.resolution = res
	return b
}

// Shards 设置分片数量，<=0 时使用自动分片策略。
func (b *Builder[K, V]) Shards(shards int) *Builder[K, V] {
	b.shards = shards
	return b
}

func (b *Builder[K, V]) NoClean() *Builder[K, V] {
	b.noClean = true
	return b
}

func (b *Builder[K, V]) OnEvict(cb EvictCallback[K, V]) *Builder[K, V] {
	b.onEvict = cb
	return b
}

func (b *Builder[K, V]) Build() Cache[K, V] {
	resolution := time.Duration(0)
	shardCount := normalizeShardCount(b.shards, b.capacity)

	if !b.noClean && b.ttl > 0 {
		if b.resolution > 0 {
			resolution = b.resolution
		} else {
			resolution = defaultResolution
		}
		resolution = normalizeWheelResolution(resolution)
	}

	return newShardedCache[K, V](b.capacity, b.ttl, resolution, b.onEvict, shardCount)
}

func normalizeShardCount(requested, capacity int) int {
	if requested <= 0 {
		requested = runtime.GOMAXPROCS(0) * 4
		if requested < 16 {
			requested = 16
		}
		if requested > 1024 {
			requested = 1024
		}
	}

	if capacity > 0 && requested > capacity {
		requested = capacity
	}
	if requested < 1 {
		requested = 1
	}

	return floorPowerOfTwo(requested)
}
