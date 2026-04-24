package gcache

import (
	"sync"
	"sync/atomic"
	"time"
)

type cacheShard[K comparable, V any] struct {
	mu         sync.Mutex
	items      map[K]uint32
	list       *ringList[K, V]
	wheel      *timeWheel
	maxEntries uint32 // 0 表示无限容量

	// 复用 buffer，消除 cleanLoop tick 级内存分配
	expiredBuf []uint32          // 过期索引 buffer
	evictedBuf []evictRecord[K, V] // 淘汰记录 buffer
}

type evictRecord[K comparable, V any] struct {
	key    K
	value  V
	reason EvictReason
}

type shardedCache[K comparable, V any] struct {
	shards     []cacheShard[K, V]
	shardMask  uint64
	defaultTTL time.Duration
	resolution time.Duration
	onEvict    EvictCallback[K, V]
	stats      stats
	count      atomic.Int64 // 原子计数器，提供 O(1) 无锁 Len()

	closeOnce sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func newShardedCache[K comparable, V any](capacity int, ttl, resolution time.Duration, onEvict EvictCallback[K, V], shardCount int) Cache[K, V] {
	bounded := capacity > 0
	if shardCount <= 0 {
		shardCount = 1
	}
	if bounded && shardCount > capacity {
		shardCount = floorPowerOfTwo(capacity)
	}
	if shardCount <= 0 {
		shardCount = 1
	}

	c := &shardedCache[K, V]{
		shards:     make([]cacheShard[K, V], shardCount),
		shardMask:  uint64(shardCount - 1),
		defaultTTL: ttl,
		resolution: resolution,
		onEvict:    onEvict,
	}

	for i := 0; i < shardCount; i++ {
		shardCap := 64
		var maxEntries uint32
		allowGrow := true
		if bounded {
			shardCap = capacity / shardCount
			if i < capacity%shardCount {
				shardCap++
			}
			if shardCap < 1 {
				shardCap = 1
			}
			maxEntries = uint32(shardCap)
			allowGrow = false
		}

		c.shards[i] = cacheShard[K, V]{
			items:      make(map[K]uint32, shardCap),
			list:       newRingList[K, V](shardCap, allowGrow),
			maxEntries: maxEntries,
		}
		if resolution > 0 && ttl > 0 {
			c.shards[i].wheel = newTimeWheel(resolution)
		}
	}

	if resolution > 0 && ttl > 0 {
		c.stopCh = make(chan struct{})
		c.doneCh = make(chan struct{})
		go c.cleanLoop()
	}

	return c
}

func (c *shardedCache[K, V]) Set(key K, value V) error {
	return c.SetWithExpire(key, value, c.defaultTTL)
}

func (c *shardedCache[K, V]) SetWithExpire(key K, value V, ttl time.Duration) error {
	hash := hash64(key)
	shard := &c.shards[shardIndexByHash(hash, c.shardMask)]

	var evicted evictRecord[K, V]
	var hasEvicted bool

	shard.mu.Lock()

	expireAt := int64(0)
	nowNano := now()
	if ttl > 0 {
		expireAt = nowNano + int64(ttl)
	}

	if idx, ok := shard.items[key]; ok {
		e := shard.list.get(idx)
		oldExpireAt := e.expireAt
		e.value = value
		e.expireAt = expireAt
		e.keyHash = hash
		shard.list.moveToFront(idx)
		if shard.wheel != nil {
			if oldExpireAt > 0 {
				shard.wheel.remove(idx, oldExpireAt)
			}
			if expireAt > 0 {
				shard.wheel.add(idx, expireAt)
			}
		}
		shard.mu.Unlock()
		return nil
	}

	if shard.maxEntries > 0 && shard.list.len() >= shard.maxEntries {
		evicted, hasEvicted = shard.evictTailLocked(EvictReasonCapacity, nowNano)
	}

	idx := shard.list.alloc()
	if idx == invalidIndex {
		shard.mu.Unlock()
		// 写入未成功时不触发回调，避免对外呈现"写入成功并淘汰"的错误语义。
		return ErrCapacityExceeded
	}

	e := shard.list.get(idx)
	e.key = key
	e.keyHash = hash
	e.value = value
	e.expireAt = expireAt

	shard.list.pushFront(idx)
	shard.items[key] = idx
	if shard.wheel != nil && expireAt > 0 {
		shard.wheel.add(idx, expireAt)
	}

	shard.mu.Unlock()

	if !hasEvicted {
		// 全新条目写入（未触发容量淘汰），总数 +1
		c.count.Add(1)
	}

	if hasEvicted {
		c.fireOnEvict(evicted)
	}

	return nil
}

func (c *shardedCache[K, V]) Get(key K) (V, error) {
	hash := hash64(key)
	shard := &c.shards[shardIndexByHash(hash, c.shardMask)]
	var zero V

	shard.mu.Lock()
	idx, ok := shard.items[key]
	if !ok {
		shard.mu.Unlock()
		c.stats.miss()
		return zero, ErrNotFound
	}

	e := shard.list.get(idx)
	nowNano := now()
	if isExpired(e.expireAt, nowNano) {
		evicted, _ := shard.removeIndexLocked(idx, EvictReasonExpired)
		shard.mu.Unlock()
		c.count.Add(-1)
		c.stats.miss()
		c.fireOnEvict(evicted)
		return zero, ErrNotFound
	}

	shard.list.moveToFront(idx)
	value := e.value
	shard.mu.Unlock()

	c.stats.hit()
	return value, nil
}

func (c *shardedCache[K, V]) GetIFPresent(key K) (V, error) {
	return c.Get(key)
}

func (c *shardedCache[K, V]) Remove(key K) bool {
	hash := hash64(key)
	shard := &c.shards[shardIndexByHash(hash, c.shardMask)]

	shard.mu.Lock()
	idx, ok := shard.items[key]
	if !ok {
		shard.mu.Unlock()
		return false
	}

	evicted, ok := shard.removeIndexLocked(idx, EvictReasonManual)
	shard.mu.Unlock()
	if ok {
		c.count.Add(-1)
		c.fireOnEvict(evicted)
	}
	return ok
}

func (c *shardedCache[K, V]) Purge(notify bool) {
	var (
		wg           sync.WaitGroup
		totalRemoved atomic.Int64
	)

	for i := range c.shards {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			shard := &c.shards[idx]

			shard.mu.Lock()

			if notify {
				// 需要回调：逐条删除并收集记录
				var evicted []evictRecord[K, V]
				cur := shard.list.head
				for cur != invalidIndex {
					next := shard.list.get(cur).next
					rec, ok := shard.removeIndexLocked(cur, EvictReasonManual)
					if ok {
						evicted = append(evicted, rec)
					}
					cur = next
				}
				if shard.wheel != nil {
					shard.wheel.clear()
				}
				removed := int64(len(evicted))
				shard.mu.Unlock()

				// 锁外逐条触发回调，避免全量汇总到全局切片再回调的内存峰值
				for _, rec := range evicted {
					c.fireOnEvict(rec)
				}
				if removed > 0 {
					totalRemoved.Add(removed)
				}
			} else {
				// 不需要回调：直接重置数据结构，O(cap) 零化 + O(1) 重置
				removed := int64(shard.list.count) // 记录实际条目数，不是总容量
				cap := uint32(len(shard.list.entries))
				shard.items = make(map[K]uint32, len(shard.items))
				shard.list.head = invalidIndex
				shard.list.tail = invalidIndex
				shard.list.free = 0
				shard.list.count = 0
				for j := uint32(0); j < cap; j++ {
					if j < cap-1 {
						shard.list.entries[j].next = j + 1
					} else {
						shard.list.entries[j].next = invalidIndex
					}
					shard.list.entries[j].prev = invalidIndex
					var zeroK K
					var zeroV V
					shard.list.entries[j].key = zeroK
					shard.list.entries[j].value = zeroV
					shard.list.entries[j].keyHash = 0
					shard.list.entries[j].expireAt = 0
				}
				if shard.wheel != nil {
					shard.wheel.clear()
				}
				shard.mu.Unlock()
				if removed > 0 {
					totalRemoved.Add(removed)
				}
			}
		}(i)
	}

	wg.Wait()

	if n := totalRemoved.Load(); n > 0 {
		c.count.Add(-n)
	}
}

func (c *shardedCache[K, V]) Len() int {
	return int(c.count.Load())
}

func (c *shardedCache[K, V]) Has(key K) bool {
	hash := hash64(key)
	shard := &c.shards[shardIndexByHash(hash, c.shardMask)]

	shard.mu.Lock()
	idx, ok := shard.items[key]
	if !ok {
		shard.mu.Unlock()
		return false
	}

	e := shard.list.get(idx)
	nowNano := now()
	if isExpired(e.expireAt, nowNano) {
		evicted, _ := shard.removeIndexLocked(idx, EvictReasonExpired)
		shard.mu.Unlock()
		c.count.Add(-1)
		c.fireOnEvict(evicted)
		return false
	}

	shard.mu.Unlock()
	return true
}

func (c *shardedCache[K, V]) Close() {
	c.closeOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
			<-c.doneCh
		}
	})
}

func (c *shardedCache[K, V]) Stats() Stats {
	return Stats{
		Hits:   c.stats.Hits(),
		Misses: c.stats.Misses(),
	}
}

func (c *shardedCache[K, V]) HitCount() uint64 {
	return c.stats.Hits()
}

func (c *shardedCache[K, V]) MissCount() uint64 {
	return c.stats.Misses()
}

func (c *shardedCache[K, V]) LookupCount() uint64 {
	return c.stats.Lookups()
}

func (c *shardedCache[K, V]) HitRate() float64 {
	return c.stats.HitRate()
}

func (c *shardedCache[K, V]) cleanLoop() {
	ticker := time.NewTicker(c.resolution)
	defer func() {
		ticker.Stop()
		close(c.doneCh)
	}()

	for {
		select {
		case <-ticker.C:
			c.cleanExpired(now())
		case <-c.stopCh:
			return
		}
	}
}

func (c *shardedCache[K, V]) cleanExpired(nowNano int64) {
	for i := range c.shards {
		shard := &c.shards[i]
		if shard.wheel == nil {
			continue
		}

		shard.mu.Lock()
		shard.wheel.popExpiredInto(nowNano, &shard.expiredBuf)
		shard.evictedBuf = shard.evictedBuf[:0]
		for _, idx := range shard.expiredBuf {
			if idx >= uint32(len(shard.list.entries)) {
				continue
			}

			e := shard.list.get(idx)
			currIdx, ok := shard.items[e.key]
			if !ok || currIdx != idx {
				continue
			}
			if e.expireAt <= 0 {
				continue
			}
			if e.expireAt > nowNano {
				// 长 TTL 可能先被窗口边界触发，未真正到期时自动续挂。
				shard.wheel.add(idx, e.expireAt)
				continue
			}

			rec, ok := shard.removeIndexLocked(idx, EvictReasonExpired)
			if ok {
				c.count.Add(-1)
				shard.evictedBuf = append(shard.evictedBuf, rec)
			}
		}
		shard.mu.Unlock()

		for _, rec := range shard.evictedBuf {
			c.fireOnEvict(rec)
		}
	}
}

func (s *cacheShard[K, V]) evictTailLocked(reason EvictReason, nowNano int64) (evictRecord[K, V], bool) {
	idx := s.list.back()
	if idx == invalidIndex {
		var empty evictRecord[K, V]
		return empty, false
	}
	if reason == EvictReasonCapacity {
		e := s.list.get(idx)
		if isExpired(e.expireAt, nowNano) {
			reason = EvictReasonExpired
		}
	}
	return s.removeIndexLocked(idx, reason)
}

func (s *cacheShard[K, V]) removeIndexLocked(idx uint32, reason EvictReason) (evictRecord[K, V], bool) {
	if idx == invalidIndex || idx >= uint32(len(s.list.entries)) {
		var empty evictRecord[K, V]
		return empty, false
	}

	e := s.list.get(idx)
	rec := evictRecord[K, V]{
		key:    e.key,
		value:  e.value,
		reason: reason,
	}

	delete(s.items, e.key)
	s.list.remove(idx)
	if s.wheel != nil && e.expireAt > 0 {
		s.wheel.remove(idx, e.expireAt)
	}
	s.list.freeEntry(idx)
	return rec, true
}

func (c *shardedCache[K, V]) fireOnEvict(rec evictRecord[K, V]) {
	if c.onEvict != nil {
		defer func() {
			// 用户回调属于不可信边界，隔离 panic 以避免影响缓存主流程。
			recover()
		}()
		c.onEvict(rec.key, rec.value, rec.reason)
	}
}

func floorPowerOfTwo(v int) int {
	if v <= 1 {
		return 1
	}
	p := 1
	for p <= v/2 {
		p <<= 1
	}
	return p
}
