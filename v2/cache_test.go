package gcache

import (
	"errors"
	"math/bits"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestBasicSetGet(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.Set("a", 1)
	cache.Set("b", 2)

	v, err := cache.Get("a")
	if err != nil || v != 1 {
		t.Errorf("expected 1, got %d, err: %v", v, err)
	}

	v, err = cache.Get("b")
	if err != nil || v != 2 {
		t.Errorf("expected 2, got %d, err: %v", v, err)
	}

	_, err = cache.Get("c")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}

	cache.Close()
}

func TestTTL(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.SetWithExpire("ttl", 1, 50*time.Millisecond)

	v, err := cache.Get("ttl")
	if err != nil || v != 1 {
		t.Errorf("expected 1, got %d, err: %v", v, err)
	}

	time.Sleep(60 * time.Millisecond)

	_, err = cache.Get("ttl")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after expiry, got: %v", err)
	}

	cache.Close()
}

func TestLRUEviction(t *testing.T) {
	cache := New[string, int](10000).Build()

	cache.Set("a", 1)
	cache.Set("b", 2)
	cache.Set("c", 3)

	for i := 0; i < 10000; i++ {
		cache.Set(string(rune('d'+i%20)), i)
	}

	if cache.Len() > 10000+3 {
		t.Errorf("expected len <= 10003, got %d", cache.Len())
	}

	cache.Close()
}

func TestLRUOrder(t *testing.T) {
	cache := New[string, int](10000).Build()

	cache.Set("a", 1)
	cache.Set("b", 2)
	cache.Set("c", 3)

	cache.Get("a")

	for i := 0; i < 10000; i++ {
		cache.Set(string(rune('d'+i%20)), i)
	}

	if !cache.Has("a") {
		t.Error("expected 'a' to be kept due to recent access")
	}

	cache.Close()
}

func TestRemove(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.Set("a", 1)

	if !cache.Remove("a") {
		t.Error("expected Remove to return true")
	}

	if cache.Remove("a") {
		t.Error("expected Remove to return false for non-existent key")
	}

	if cache.Has("a") {
		t.Error("expected 'a' to be removed")
	}

	cache.Close()
}

func TestPurge(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.Set("a", 1)
	cache.Set("b", 2)
	cache.Set("c", 3)

	cache.Purge()

	if cache.Len() != 0 {
		t.Errorf("expected len 0 after purge, got %d", cache.Len())
	}

	cache.Close()
}

func TestEvictCallback(t *testing.T) {
	var mu sync.Mutex
	var evicted []struct {
		key    string
		value  int
		reason EvictReason
	}

	cache := New[string, int](1000).
		OnEvict(func(key string, value int, reason EvictReason) {
			mu.Lock()
			defer mu.Unlock()
			evicted = append(evicted, struct {
				key    string
				value  int
				reason EvictReason
			}{key, value, reason})
		}).
		Build()

	for i := 0; i < 2000; i++ {
		cache.Set("key:"+strconv.Itoa(i), i)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(evicted) == 0 {
		t.Error("expected some evictions")
	}

	hasCapacityEvict := false
	for _, e := range evicted {
		if e.reason == EvictReasonCapacity {
			hasCapacityEvict = true
			break
		}
	}
	if !hasCapacityEvict {
		t.Error("expected capacity eviction")
	}

	cache.Close()
}

func TestExpireCallback(t *testing.T) {
	var expired []string

	cache := New[string, int](100).
		OnEvict(func(key string, value int, reason EvictReason) {
			if reason == EvictReasonExpired {
				expired = append(expired, key)
			}
		}).
		Build()

	cache.SetWithExpire("ttl", 1, 30*time.Millisecond)

	time.Sleep(50 * time.Millisecond)

	cache.Get("ttl")

	if len(expired) != 1 || expired[0] != "ttl" {
		t.Errorf("expected 'ttl' to be expired, got: %v", expired)
	}

	cache.Close()
}

func TestConcurrentAccess(t *testing.T) {
	cache := New[int, int](1000).Build()

	var wg sync.WaitGroup
	n := 100

	for i := 0; i < n; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Set(id*1000+j, j)
			}
		}(i)

		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Get(id * 1000)
			}
		}(i)
	}

	wg.Wait()

	cache.Close()
}

func TestCleanExpired(t *testing.T) {
	evicted := make(chan string, 10)

	cache := New[string, int](100).
		TTL(30 * time.Millisecond).
		Resolution(20 * time.Millisecond).
		OnEvict(func(key string, value int, reason EvictReason) {
			if reason == EvictReasonExpired {
				select {
				case evicted <- key:
				default:
				}
			}
		}).
		Build()

	cache.Set("a", 1)
	cache.Set("b", 2)

	time.Sleep(150 * time.Millisecond)

	count := 0
	for {
		select {
		case <-evicted:
			count++
		default:
			goto done
		}
	}
done:
	if count < 2 {
		t.Errorf("expected at least 2 expired callbacks, got %d", count)
	}

	cache.Close()
}

func TestDefaultCleanEnabled(t *testing.T) {
	evicted := make(chan string, 10)

	cache := New[string, int](100).
		TTL(30 * time.Millisecond).
		Resolution(20 * time.Millisecond).
		OnEvict(func(key string, value int, reason EvictReason) {
			if reason == EvictReasonExpired {
				select {
				case evicted <- key:
				default:
				}
			}
		}).
		Build()

	cache.Set("a", 1)
	cache.Set("b", 2)

	time.Sleep(150 * time.Millisecond)

	count := 0
	for {
		select {
		case <-evicted:
			count++
		default:
			goto done
		}
	}
done:
	if count < 2 {
		t.Errorf("expected at least 2 expired callbacks, got %d", count)
	}

	cache.Close()
}

func TestLongTTLRescheduleInRingWheel(t *testing.T) {
	originNow := now
	defer func() {
		now = originNow
	}()

	var fakeNow int64
	now = func() int64 {
		return fakeNow
	}

	cache := New[string, int](64).
		TTL(48 * time.Hour).
		Resolution(time.Minute).
		Build()
	defer cache.Close()

	if err := cache.Set("long", 1); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	c := cache.(*shardedCache[string, int])

	// 超过 24h 窗口后触发一次清理，条目应被自动续挂而不是过期。
	fakeNow = int64(24*time.Hour + time.Minute)
	c.cleanExpired(fakeNow)
	if v, err := cache.Get("long"); err != nil || v != 1 {
		t.Fatalf("expected value kept after first rollover, got v=%d err=%v", v, err)
	}

	// 到达真实过期时间后，清理应将其淘汰。
	fakeNow = int64(48*time.Hour + time.Minute)
	c.cleanExpired(fakeNow)
	if cache.Has("long") {
		t.Fatal("expected key expired after real ttl")
	}
}

func TestNoClean(t *testing.T) {
	cache := New[string, int](100).
		TTL(30 * time.Millisecond).
		NoClean().
		Build()

	cache.Set("a", 1)
	cache.Set("b", 2)

	time.Sleep(100 * time.Millisecond)

	// Without cleaner, items should still be in cache
	if cache.Len() != 2 {
		t.Errorf("expected len 2 without cleaner, got %d", cache.Len())
	}

	cache.Close()
}

func TestHas(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.Set("a", 1)

	if !cache.Has("a") {
		t.Error("expected Has('a') to return true")
	}

	if cache.Has("b") {
		t.Error("expected Has('b') to return false")
	}

	cache.SetWithExpire("c", 3, 30*time.Millisecond)

	if !cache.Has("c") {
		t.Error("expected Has('c') to return true before expiry")
	}

	time.Sleep(50 * time.Millisecond)

	if cache.Has("c") {
		t.Error("expected Has('c') to return false after expiry")
	}

	cache.Close()
}

func TestStats(t *testing.T) {
	cache := New[string, int](100).Build()

	cache.Set("a", 1)

	cache.Get("a")
	cache.Get("a")
	cache.Get("b")

	c := cache.(*shardedCache[string, int])
	stats := c.Stats()

	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}

	cache.Close()
}

func TestIntKey(t *testing.T) {
	cache := New[int, string](100).Build()

	cache.Set(1, "one")
	cache.Set(2, "two")

	v, err := cache.Get(1)
	if err != nil || v != "one" {
		t.Errorf("expected 'one', got '%s', err: %v", v, err)
	}

	cache.Close()
}

func TestStructKeyHashConsistency(t *testing.T) {
	type userKey struct {
		ID   int
		Name string
	}

	cache := New[userKey, int](100).Build()
	defer cache.Close()

	s1 := string([]byte{'a', 'l', 'i', 'c', 'e'})
	s2 := string([]byte{'a', 'l', 'i', 'c', 'e'})
	if s1 != s2 {
		t.Fatal("expected same string content")
	}

	k1 := userKey{ID: 7, Name: s1}
	k2 := userKey{ID: 7, Name: s2}
	if k1 != k2 {
		t.Fatal("expected keys to be equal")
	}

	if err := cache.Set(k1, 42); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	v, err := cache.Get(k2)
	if err != nil || v != 42 {
		t.Fatalf("expected value hit with equal key, got v=%d err=%v", v, err)
	}
}

func TestEvictCallbackPanicIsolation(t *testing.T) {
	cache := New[string, int](1).
		OnEvict(func(key string, value int, reason EvictReason) {
			panic("callback panic")
		}).
		Build()
	defer cache.Close()

	if err := cache.Set("a", 1); err != nil {
		t.Fatalf("set a failed: %v", err)
	}

	// 容量淘汰会触发回调，回调 panic 需要被缓存内部隔离。
	if err := cache.Set("b", 2); err != nil {
		t.Fatalf("set b failed: %v", err)
	}

	v, err := cache.Get("b")
	if err != nil || v != 2 {
		t.Fatalf("expected key b still available, got v=%d err=%v", v, err)
	}

	// 手动删除也会触发回调，仍应不影响调用方。
	if ok := cache.Remove("b"); !ok {
		t.Fatal("expected remove b success")
	}
}

func TestStructValue(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}

	cache := New[string, User](100).Build()

	cache.Set("alice", User{Name: "Alice", Age: 30})

	v, err := cache.Get("alice")
	if err != nil || v.Name != "Alice" || v.Age != 30 {
		t.Errorf("unexpected result: %+v, err: %v", v, err)
	}

	cache.Close()
}

func TestUnlimitedCapacityNoEviction(t *testing.T) {
	cache := New[string, int](0).Build()
	defer cache.Close()

	total := 5000
	for i := 0; i < total; i++ {
		key := "u:" + strconv.Itoa(i)
		if err := cache.Set(key, i); err != nil {
			t.Fatalf("set failed: %v", err)
		}
	}

	if cache.Len() != total {
		t.Fatalf("expected len %d, got %d", total, cache.Len())
	}

	v, err := cache.Get("u:0")
	if err != nil || v != 0 {
		t.Fatalf("expected first key kept, got v=%d err=%v", v, err)
	}
}

func TestUnlimitedCapacityShardCountNotCappedByCapacity(t *testing.T) {
	cache := New[int, int](0).Shards(300).Build()
	defer cache.Close()

	c := cache.(*shardedCache[int, int])
	// 300 会被归一化为 2 的幂（向下）：256
	if got := len(c.shards); got != 256 {
		t.Fatalf("expected 256 shards, got %d", got)
	}
}

func TestSetReturnsErrCapacityExceededOnAllocFailure(t *testing.T) {
	evictCalled := false
	cache := New[string, int](1).
		Shards(1).
		OnEvict(func(key string, value int, reason EvictReason) {
			evictCalled = true
		}).
		Build()
	defer cache.Close()

	c := cache.(*shardedCache[string, int])
	shard := &c.shards[0]
	shard.mu.Lock()
	// 人工制造“无空闲索引”异常路径，验证错误语义与回调语义。
	shard.list.free = invalidIndex
	shard.mu.Unlock()

	err := cache.Set("broken", 1)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("expected ErrCapacityExceeded, got %v", err)
	}
	if evictCalled {
		t.Fatal("expected no eviction callback when write failed")
	}
}

func TestCapacityEvictionUsesExpiredReasonWhenTailExpired(t *testing.T) {
	var (
		mu      sync.Mutex
		reasons []EvictReason
	)

	cache := New[string, int](1).
		Shards(1).
		NoClean().
		OnEvict(func(key string, value int, reason EvictReason) {
			mu.Lock()
			reasons = append(reasons, reason)
			mu.Unlock()
		}).
		Build()
	defer cache.Close()

	if err := cache.SetWithExpire("a", 1, 20*time.Millisecond); err != nil {
		t.Fatalf("set a failed: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	if err := cache.Set("b", 2); err != nil {
		t.Fatalf("set b failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 1 {
		t.Fatalf("expected 1 eviction callback, got %d", len(reasons))
	}
	if reasons[0] != EvictReasonExpired {
		t.Fatalf("expected eviction reason expired, got %s", reasons[0])
	}
}

func TestTimeWheelSlotIndexAlwaysNonNegative(t *testing.T) {
	tw := newTimeWheel(time.Second)
	got := tw.slotIndex(-1)
	if got < 0 || got >= tw.slotCount {
		t.Fatalf("expected normalized index in [0, %d), got %d", tw.slotCount, got)
	}
}

func TestTimeWheelRollbackResetsLastProcessedSlot(t *testing.T) {
	tw := newTimeWheel(time.Second)
	tw.lastProcessedSlot = 100

	expired := tw.popExpired(50 * int64(time.Second))
	if len(expired) != 0 {
		t.Fatalf("expected no expired entries, got %d", len(expired))
	}
	if tw.lastProcessedSlot != 49 {
		t.Fatalf("expected lastProcessedSlot reset to 49, got %d", tw.lastProcessedSlot)
	}
}

func TestTimeWheelLargeGapCollectsHistoricalMappedSlots(t *testing.T) {
	tw := newTimeWheel(time.Second)
	tw.lastProcessedSlot = 0

	const expiredIdx uint32 = 1
	const futureIdx uint32 = 2

	oldSlot := int64(10)
	currentSlot := tw.slotCount + 1000
	futureSlot := currentSlot + 5

	oldBucket := tw.slotIndex(oldSlot)
	tw.slots[oldBucket] = map[uint32]struct{}{expiredIdx: {}}
	tw.indexToSlot[expiredIdx] = oldSlot

	futureBucket := tw.slotIndex(futureSlot)
	if tw.slots[futureBucket] == nil {
		tw.slots[futureBucket] = make(map[uint32]struct{})
	}
	tw.slots[futureBucket][futureIdx] = struct{}{}
	tw.indexToSlot[futureIdx] = futureSlot

	expired := tw.popExpired(currentSlot * tw.resolution)

	foundExpired := false
	for _, idx := range expired {
		if idx == expiredIdx {
			foundExpired = true
		}
		if idx == futureIdx {
			t.Fatal("future mapped slot should not be returned as expired")
		}
	}
	if !foundExpired {
		t.Fatal("expected historical mapped slot to be collected after large gap")
	}
	if _, ok := tw.indexToSlot[expiredIdx]; ok {
		t.Fatal("expected historical mapped slot removed from indexToSlot")
	}
	if _, ok := tw.indexToSlot[futureIdx]; !ok {
		t.Fatal("expected future mapped slot kept in indexToSlot")
	}
	if tw.lastProcessedSlot != currentSlot {
		t.Fatalf("expected lastProcessedSlot=%d, got %d", currentSlot, tw.lastProcessedSlot)
	}
}

func TestFloorPowerOfTwoMaxInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	got := floorPowerOfTwo(maxInt)
	expected := 1 << (bits.Len(uint(maxInt)) - 1)

	if got != expected {
		t.Fatalf("expected %d, got %d", expected, got)
	}
	if got <= 0 || got > maxInt {
		t.Fatalf("expected result in (0, %d], got %d", maxInt, got)
	}
	if got&(got-1) != 0 {
		t.Fatalf("expected power of two, got %d", got)
	}
}

func TestNormalizeWheelResolutionCapsTinyInput(t *testing.T) {
	got := normalizeWheelResolution(time.Nanosecond)
	minRes := time.Duration(ceilDiv64(int64(defaultWheelWindow), maxWheelSlots))

	if got != minRes {
		t.Fatalf("expected normalized resolution %v, got %v", minRes, got)
	}
	slotCount := ceilDiv64(int64(defaultWheelWindow), int64(got))
	if slotCount > maxWheelSlots {
		t.Fatalf("expected slotCount <= %d, got %d", maxWheelSlots, slotCount)
	}
}
