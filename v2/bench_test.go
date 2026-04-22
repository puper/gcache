package gcache

import (
	"math/rand"
	"strconv"
	"sync/atomic"
	"testing"
)

const benchKeySpace = 100000

func buildBenchStringKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "user:" + strconv.Itoa(i)
	}
	return keys
}

func BenchmarkSet(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cache.Set(i%benchKeySpace, i)
	}

	cache.Close()
}

func BenchmarkGetHit(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	for i := 0; i < benchKeySpace; i++ {
		_ = cache.Set(i, i)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Get(i % benchKeySpace)
	}

	cache.Close()
}

func BenchmarkGetMiss(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Get(i + benchKeySpace)
	}

	cache.Close()
}

func BenchmarkSetParallel(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = cache.Set(i%benchKeySpace, i)
			i++
		}
	})

	cache.Close()
}

func BenchmarkGetParallel(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	for i := 0; i < benchKeySpace; i++ {
		_ = cache.Set(i, i)
	}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = cache.Get(i % benchKeySpace)
			i++
		}
	})

	cache.Close()
}

func BenchmarkMixedParallel(b *testing.B) {
	cache := New[int, int](benchKeySpace).Build()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				_ = cache.Set(i%benchKeySpace, i)
			} else {
				_, _ = cache.Get(i % benchKeySpace)
			}
			i++
		}
	})

	cache.Close()
}

func BenchmarkStringKeyHighCardinality(b *testing.B) {
	keys := buildBenchStringKeys(benchKeySpace)
	cache := New[string, int](benchKeySpace).Build()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cache.Set(keys[i%len(keys)], i)
	}

	cache.Close()
}

func BenchmarkStringKeyParallelHighCardinality(b *testing.B) {
	keys := buildBenchStringKeys(benchKeySpace)
	cache := New[string, int](benchKeySpace).Build()
	for i := 0; i < len(keys); i++ {
		_ = cache.Set(keys[i], i)
	}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		var x uint64 = 88172645463325252
		for pb.Next() {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			_, _ = cache.Get(keys[int(x%uint64(len(keys)))])
		}
	})

	cache.Close()
}

func BenchmarkZipfGetParallel(b *testing.B) {
	keys := buildBenchStringKeys(benchKeySpace)
	cache := New[string, int](benchKeySpace).Shards(256).Build()
	for i := 0; i < len(keys); i++ {
		_ = cache.Set(keys[i], i)
	}
	b.ResetTimer()

	var seedSeq atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		seed := seedSeq.Add(1)
		r := rand.New(rand.NewSource(seed + 42))
		zipf := rand.NewZipf(r, 1.2, 3, uint64(len(keys)-1))
		for pb.Next() {
			idx := zipf.Uint64()
			_, _ = cache.Get(keys[idx])
		}
	})

	cache.Close()
}

func BenchmarkReadHeavyParallel(b *testing.B) {
	cache := New[int, int](benchKeySpace).Shards(256).Build()
	for i := 0; i < benchKeySpace; i++ {
		_ = cache.Set(i, i)
	}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				_ = cache.Set(i%benchKeySpace, i)
			} else {
				_, _ = cache.Get(i % benchKeySpace)
			}
			i++
		}
	})

	cache.Close()
}
