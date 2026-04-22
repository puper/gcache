package gcache

import (
    "testing"
)

func BenchmarkLRUSet(b *testing.B) {
    gc := New(10000).LRU().Build()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Set(i, i)
    }
}

func BenchmarkLRUGetHit(b *testing.B) {
    gc := New(10000).LRU().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Get(i % 10000)
    }
}

func BenchmarkLFUSet(b *testing.B) {
    gc := New(10000).LFU().Build()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Set(i, i)
    }
}

func BenchmarkLFUGetHit(b *testing.B) {
    gc := New(10000).LFU().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Get(i % 10000)
    }
}

func BenchmarkARCSet(b *testing.B) {
    gc := New(10000).ARC().Build()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Set(i, i)
    }
}

func BenchmarkARCGetHit(b *testing.B) {
    gc := New(10000).ARC().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Get(i % 10000)
    }
}

func BenchmarkSimpleSet(b *testing.B) {
    gc := New(10000).Simple().Build()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Set(i, i)
    }
}

func BenchmarkSimpleGetHit(b *testing.B) {
    gc := New(10000).Simple().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        gc.Get(i % 10000)
    }
}

// 并发测试
func BenchmarkLRUGetParallel(b *testing.B) {
    gc := New(10000).LRU().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            gc.Get(i % 10000)
            i++
        }
    })
}

func BenchmarkLFUGetParallel(b *testing.B) {
    gc := New(10000).LFU().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            gc.Get(i % 10000)
            i++
        }
    })
}

func BenchmarkSimpleGetParallel(b *testing.B) {
    gc := New(10000).Simple().Build()
    for i := 0; i < 10000; i++ {
        gc.Set(i, i)
    }
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            gc.Get(i % 10000)
            i++
        }
    })
}
