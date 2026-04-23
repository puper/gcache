package gcache

import "sync/atomic"

const (
	// 无效索引
	invalidIndex = ^uint32(0)
)

// entry 缓存条目
type entry[K comparable, V any] struct {
	key      K
	keyHash  uint64
	value    V
	expireAt int64
	prev     uint32
	next     uint32
}

// ringList 环形双向链表（预分配数组实现）
type ringList[K comparable, V any] struct {
	entries   []entry[K, V]
	head      uint32
	tail      uint32
	free      uint32
	count     uint32
	allowGrow bool
}

// newRingList 创建环形链表
func newRingList[K comparable, V any](capacity int, allowGrow bool) *ringList[K, V] {
	if capacity <= 0 {
		capacity = 1
	}

	r := &ringList[K, V]{
		entries:   make([]entry[K, V], capacity),
		head:      invalidIndex,
		tail:      invalidIndex,
		free:      0,
		allowGrow: allowGrow,
	}

	for i := 0; i < capacity; i++ {
		if i < capacity-1 {
			r.entries[i].next = uint32(i + 1)
		} else {
			r.entries[i].next = invalidIndex
		}
		r.entries[i].prev = invalidIndex
	}

	return r
}

// alloc 分配一个新条目，返回索引
func (r *ringList[K, V]) alloc() uint32 {
	if r.free == invalidIndex {
		if !r.allowGrow {
			return invalidIndex
		}
		if !r.grow() {
			return invalidIndex
		}
	}
	idx := r.free
	e := &r.entries[idx]
	r.free = e.next
	e.next = invalidIndex
	e.prev = invalidIndex
	r.count++
	return idx
}

// free 释放条目
func (r *ringList[K, V]) freeEntry(idx uint32) {
	r.mustValidIndex(idx)
	e := &r.entries[idx]
	var zeroK K
	var zeroV V
	e.key = zeroK
	e.value = zeroV
	e.keyHash = 0
	e.expireAt = 0
	e.next = r.free
	e.prev = invalidIndex
	r.free = idx
	r.count--
}

// pushFront 将条目插入链表头部
func (r *ringList[K, V]) pushFront(idx uint32) {
	r.mustValidIndex(idx)
	e := &r.entries[idx]
	e.prev = invalidIndex
	e.next = r.head

	if r.head != invalidIndex {
		r.entries[r.head].prev = idx
	}
	r.head = idx

	if r.tail == invalidIndex {
		r.tail = idx
	}
}

// remove 从链表中移除条目
func (r *ringList[K, V]) remove(idx uint32) {
	r.mustValidIndex(idx)
	e := &r.entries[idx]

	if e.prev != invalidIndex {
		r.entries[e.prev].next = e.next
	} else {
		r.head = e.next
	}

	if e.next != invalidIndex {
		r.entries[e.next].prev = e.prev
	} else {
		r.tail = e.prev
	}

	e.prev = invalidIndex
	e.next = invalidIndex
}

// moveToFront 将条目移动到链表头部
func (r *ringList[K, V]) moveToFront(idx uint32) {
	if r.head == idx {
		return
	}
	r.remove(idx)
	r.pushFront(idx)
}

// back 返回链表尾部索引
func (r *ringList[K, V]) back() uint32 {
	return r.tail
}

// len 返回链表长度
func (r *ringList[K, V]) len() uint32 {
	return r.count
}

// cap 返回链表容量
func (r *ringList[K, V]) cap() int {
	return len(r.entries)
}

// get 获取条目
func (r *ringList[K, V]) get(idx uint32) *entry[K, V] {
	r.mustValidIndex(idx)
	return &r.entries[idx]
}

func (r *ringList[K, V]) mustValidIndex(idx uint32) {
	if idx >= uint32(len(r.entries)) {
		panic("gcache: ringList index out of range")
	}
}

func (r *ringList[K, V]) grow() bool {
	oldCap := len(r.entries)
	if oldCap <= 0 {
		oldCap = 1
	}
	maxByIndex := uint64(^uint32(0))
	maxByInt := uint64(int(^uint(0) >> 1))
	maxCap := maxByIndex
	if maxByInt < maxCap {
		maxCap = maxByInt
	}
	if uint64(oldCap) >= maxCap {
		return false
	}

	growBy := oldCap
	if growBy < 64 {
		growBy = 64
	}

	newCap64 := uint64(oldCap) + uint64(growBy)
	if newCap64 > maxCap {
		newCap64 = maxCap
	}
	if newCap64 <= uint64(oldCap) {
		return false
	}
	// 32 位系统防护：确保 newCap 不超过 int 最大值且大于 oldCap
	newCap := int(newCap64)
	if newCap <= oldCap {
		return false
	}

	r.entries = append(r.entries, make([]entry[K, V], newCap-oldCap)...)

	start := oldCap
	end := newCap - 1
	for i := start; i <= end; i++ {
		if i < end {
			r.entries[i].next = uint32(i + 1)
		} else {
			r.entries[i].next = invalidIndex
		}
		r.entries[i].prev = invalidIndex
	}

	r.free = uint32(start)
	return true
}

// stats 统计信息
type stats struct {
	hits   atomic.Uint64
	misses atomic.Uint64
}

func (s *stats) hit() {
	s.hits.Add(1)
}

func (s *stats) miss() {
	s.misses.Add(1)
}

func (s *stats) Hits() uint64 {
	return s.hits.Load()
}

func (s *stats) Misses() uint64 {
	return s.misses.Load()
}

func (s *stats) Lookups() uint64 {
	return s.Hits() + s.Misses()
}

func (s *stats) HitRate() float64 {
	h, m := s.Hits(), s.Misses()
	total := h + m
	if total == 0 {
		return 0
	}
	return float64(h) / float64(total)
}
