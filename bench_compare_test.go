// Package gcache - 性能对比测试：gcache v1/v2 vs freecache vs bigcache
package gcache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/coocood/freecache"
	v2 "github.com/puper/gcache/v2"
)

const (
	benchCapacity   = 100000 // 10万条目
	benchKeySpace   = 100000
	benchShardCount = 256
)

// ============================================================================
// GCache v1 - LRU
// ============================================================================

func BenchmarkV1LRU_Set(b *testing.B) {
	cache := New(benchCapacity).LRU().Build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(i%benchKeySpace, i)
	}
}

func BenchmarkV1LRU_GetHit(b *testing.B) {
	cache := New(benchCapacity).LRU().Build()
	for i := 0; i < benchKeySpace; i++ {
		cache.Set(i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(i % benchKeySpace)
	}
}

func BenchmarkV1LRU_GetParallel(b *testing.B) {
	cache := New(benchCapacity).LRU().Build()
	for i := 0; i < benchKeySpace; i++ {
		cache.Set(i, i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get(i % benchKeySpace)
			i++
		}
	})
}

func BenchmarkV1LRU_SetParallel(b *testing.B) {
	cache := New(benchCapacity).LRU().Build()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Set(i%benchKeySpace, i)
			i++
		}
	})
}

func BenchmarkV1LRU_MixedParallel(b *testing.B) {
	cache := New(benchCapacity).LRU().Build()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				cache.Set(i%benchKeySpace, i)
			} else {
				cache.Get(i % benchKeySpace)
			}
			i++
		}
	})
}

// ============================================================================
// GCache v2 - 泛型 LRU
// ============================================================================

func BenchmarkV2_Set(b *testing.B) {
	cache := v2.New[int, int](benchCapacity).Shards(benchShardCount).Build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(i%benchKeySpace, i)
	}
	cache.Close()
}

func BenchmarkV2_GetHit(b *testing.B) {
	cache := v2.New[int, int](benchCapacity).Shards(benchShardCount).Build()
	for i := 0; i < benchKeySpace; i++ {
		cache.Set(i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(i % benchKeySpace)
	}
	cache.Close()
}

func BenchmarkV2_GetParallel(b *testing.B) {
	cache := v2.New[int, int](benchCapacity).Shards(benchShardCount).Build()
	for i := 0; i < benchKeySpace; i++ {
		cache.Set(i, i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get(i % benchKeySpace)
			i++
		}
	})
	cache.Close()
}

func BenchmarkV2_SetParallel(b *testing.B) {
	cache := v2.New[int, int](benchCapacity).Shards(benchShardCount).Build()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Set(i%benchKeySpace, i)
			i++
		}
	})
	cache.Close()
}

func BenchmarkV2_MixedParallel(b *testing.B) {
	cache := v2.New[int, int](benchCapacity).Shards(benchShardCount).Build()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				cache.Set(i%benchKeySpace, i)
			} else {
				cache.Get(i % benchKeySpace)
			}
			i++
		}
	})
	cache.Close()
}

// ============================================================================
// FreeCache
// ============================================================================

func BenchmarkFreeCache_Set(b *testing.B) {
	// freecache 容量单位是字节，假设每个条目约 24 字节
	cache := freecache.NewCache(benchCapacity * 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(strconv.Itoa(i % benchKeySpace))
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val, 0)
	}
}

func BenchmarkFreeCache_GetHit(b *testing.B) {
	cache := freecache.NewCache(benchCapacity * 32)
	for i := 0; i < benchKeySpace; i++ {
		key := []byte(strconv.Itoa(i))
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val, 0)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(strconv.Itoa(i % benchKeySpace))
		cache.Get(key)
	}
}

func BenchmarkFreeCache_GetParallel(b *testing.B) {
	cache := freecache.NewCache(benchCapacity * 32)
	for i := 0; i < benchKeySpace; i++ {
		key := []byte(strconv.Itoa(i))
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val, 0)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(strconv.Itoa(i % benchKeySpace))
			cache.Get(key)
			i++
		}
	})
}

func BenchmarkFreeCache_SetParallel(b *testing.B) {
	cache := freecache.NewCache(benchCapacity * 32)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(strconv.Itoa(i % benchKeySpace))
			val := []byte(strconv.Itoa(i))
			cache.Set(key, val, 0)
			i++
		}
	})
}

func BenchmarkFreeCache_MixedParallel(b *testing.B) {
	cache := freecache.NewCache(benchCapacity * 32)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				key := []byte(strconv.Itoa(i % benchKeySpace))
				val := []byte(strconv.Itoa(i))
				cache.Set(key, val, 0)
			} else {
				key := []byte(strconv.Itoa(i % benchKeySpace))
				cache.Get(key)
			}
			i++
		}
	})
}

// ============================================================================
// BigCache
// ============================================================================

func BenchmarkBigCache_Set(b *testing.B) {
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	config.MaxEntriesInWindow = benchCapacity
	config.MaxEntrySize = 256
	cache, _ := bigcache.New(context.Background(), config)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := strconv.Itoa(i % benchKeySpace)
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val)
	}
	cache.Close()
}

func BenchmarkBigCache_GetHit(b *testing.B) {
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	config.MaxEntriesInWindow = benchCapacity
	config.MaxEntrySize = 256
	cache, _ := bigcache.New(context.Background(), config)
	for i := 0; i < benchKeySpace; i++ {
		key := strconv.Itoa(i)
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := strconv.Itoa(i % benchKeySpace)
		cache.Get(key)
	}
	cache.Close()
}

func BenchmarkBigCache_GetParallel(b *testing.B) {
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	config.MaxEntriesInWindow = benchCapacity
	config.MaxEntrySize = 256
	cache, _ := bigcache.New(context.Background(), config)
	for i := 0; i < benchKeySpace; i++ {
		key := strconv.Itoa(i)
		val := []byte(strconv.Itoa(i))
		cache.Set(key, val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := strconv.Itoa(i % benchKeySpace)
			cache.Get(key)
			i++
		}
	})
	cache.Close()
}

func BenchmarkBigCache_SetParallel(b *testing.B) {
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	config.MaxEntriesInWindow = benchCapacity
	config.MaxEntrySize = 256
	cache, _ := bigcache.New(context.Background(), config)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := strconv.Itoa(i % benchKeySpace)
			val := []byte(strconv.Itoa(i))
			cache.Set(key, val)
			i++
		}
	})
	cache.Close()
}

func BenchmarkBigCache_MixedParallel(b *testing.B) {
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	config.MaxEntriesInWindow = benchCapacity
	config.MaxEntrySize = 256
	cache, _ := bigcache.New(context.Background(), config)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				key := strconv.Itoa(i % benchKeySpace)
				val := []byte(strconv.Itoa(i))
				cache.Set(key, val)
			} else {
				key := strconv.Itoa(i % benchKeySpace)
				cache.Get(key)
			}
			i++
		}
	})
	cache.Close()
}

// ============================================================================
// String Key 测试 - 更真实的场景
// ============================================================================

func buildStringKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "user:" + strconv.Itoa(i)
	}
	return keys
}

func BenchmarkV2_StringKey_GetParallel(b *testing.B) {
	keys := buildStringKeys(benchKeySpace)
	cache := v2.New[string, int](benchCapacity).Shards(benchShardCount).Build()
	for i, key := range keys {
		cache.Set(key, i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get(keys[i%benchKeySpace])
			i++
		}
	})
	cache.Close()
}

func BenchmarkFreeCache_StringKey_GetParallel(b *testing.B) {
	keys := buildStringKeys(benchKeySpace)
	cache := freecache.NewCache(benchCapacity * 64)
	for i, key := range keys {
		cache.Set([]byte(key), []byte(strconv.Itoa(i)), 0)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get([]byte(keys[i%benchKeySpace]))
			i++
		}
	})
}

func BenchmarkBigCache_StringKey_GetParallel(b *testing.B) {
	keys := buildStringKeys(benchKeySpace)
	config := bigcache.DefaultConfig(10 * time.Minute)
	config.Shards = benchShardCount
	cache, _ := bigcache.New(context.Background(), config)
	for i, key := range keys {
		cache.Set(key, []byte(strconv.Itoa(i)))
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get(keys[i%benchKeySpace])
			i++
		}
	})
	cache.Close()
}
