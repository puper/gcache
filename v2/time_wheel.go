package gcache

import "time"

const defaultWheelWindow = 24 * time.Hour

const maxWheelSlots int64 = 1 << 24

type timeWheel struct {
	slots             []map[uint32]struct{}
	indexToSlot       map[uint32]int64
	resolution        int64
	slotCount         int64
	lastProcessedSlot int64
}

func newTimeWheel(resolution time.Duration) *timeWheel {
	resolution = normalizeWheelResolution(resolution)

	resNanos := int64(resolution)
	slotCount := ceilDiv64(int64(defaultWheelWindow), resNanos)
	if slotCount < 1 {
		slotCount = 1
	}

	currentSlot := now() / resNanos

	return &timeWheel{
		slots:             make([]map[uint32]struct{}, slotCount),
		indexToSlot:       make(map[uint32]int64),
		resolution:        resNanos,
		slotCount:         slotCount,
		lastProcessedSlot: currentSlot - 1,
	}
}

func normalizeWheelResolution(resolution time.Duration) time.Duration {
	if resolution <= 0 {
		return time.Second
	}
	if resolution > defaultWheelWindow {
		return defaultWheelWindow
	}

	resNanos := int64(resolution)
	slotCount := ceilDiv64(int64(defaultWheelWindow), resNanos)
	if slotCount <= maxWheelSlots {
		return resolution
	}

	minResNanos := ceilDiv64(int64(defaultWheelWindow), maxWheelSlots)
	return time.Duration(minResNanos)
}

func ceilDiv64(a, b int64) int64 {
	return (a + b - 1) / b
}

func (tw *timeWheel) add(idx uint32, expireAtNano int64) {
	if expireAtNano <= 0 {
		return
	}

	// idx 可能被复用，先删除旧位置避免脏引用。
	if oldSlot, ok := tw.indexToSlot[idx]; ok {
		bucket := tw.slots[tw.slotIndex(oldSlot)]
		if bucket != nil {
			delete(bucket, idx)
			if len(bucket) == 0 {
				tw.slots[tw.slotIndex(oldSlot)] = nil
			}
		}
	}

	slotKey := tw.scheduleSlot(expireAtNano)
	bucketIdx := tw.slotIndex(slotKey)
	bucket := tw.slots[bucketIdx]
	if bucket == nil {
		bucket = make(map[uint32]struct{})
		tw.slots[bucketIdx] = bucket
	}
	bucket[idx] = struct{}{}
	tw.indexToSlot[idx] = slotKey
}

// remove 主动删除索引，减少后续扫描候选数量。
func (tw *timeWheel) remove(idx uint32, _ int64) {
	slotKey, ok := tw.indexToSlot[idx]
	if !ok {
		return
	}

	bucketIdx := tw.slotIndex(slotKey)
	bucket := tw.slots[bucketIdx]
	if bucket != nil {
		delete(bucket, idx)
		if len(bucket) == 0 {
			tw.slots[bucketIdx] = nil
		}
	}
	delete(tw.indexToSlot, idx)
}

func (tw *timeWheel) popExpiredInto(nowNano int64, buf *[]uint32) {
	currentSlot := nowNano / tw.resolution
	*buf = (*buf)[:0]

	start := tw.lastProcessedSlot + 1
	if start > currentSlot {
		// 系统时间回拨时重置推进点，避免长期跳过过期扫描。
		tw.lastProcessedSlot = currentSlot - 1
		return
	}
	// 若系统长时间停顿导致跨度过大，仅扫描一个窗口长度的 slot。
	// 对于更旧的 mappedSlot，后续通过 mappedSlot < start 条件进行兜底回收。
	if span := currentSlot - start + 1; span > tw.slotCount {
		start = currentSlot - tw.slotCount + 1
	}

	for slotKey := start; slotKey <= currentSlot; slotKey++ {
		bucketIdx := tw.slotIndex(slotKey)
		bucket := tw.slots[bucketIdx]
		if bucket == nil {
			continue
		}

		for idx := range bucket {
			mappedSlot, ok := tw.indexToSlot[idx]
			if !ok {
				delete(bucket, idx)
				continue
			}
			// mappedSlot==slotKey：常规到期槽位；
			// mappedSlot<start：大跨度追赶时的历史槽位，需兜底处理；
			// mappedSlot>currentSlot：未来槽位，保留等待后续处理。
			if mappedSlot > currentSlot {
				continue
			}
			if mappedSlot != slotKey && mappedSlot >= start {
				continue
			}

			*buf = append(*buf, idx)
			delete(bucket, idx)
			delete(tw.indexToSlot, idx)
		}

		if len(bucket) == 0 {
			tw.slots[bucketIdx] = nil
		}
	}

	tw.lastProcessedSlot = currentSlot
}

func (tw *timeWheel) clear() {
	tw.slots = make([]map[uint32]struct{}, tw.slotCount)
	tw.indexToSlot = make(map[uint32]int64)
	tw.lastProcessedSlot = now() / tw.resolution
}

func (tw *timeWheel) slotIndex(slotKey int64) int64 {
	idx := slotKey % tw.slotCount
	if idx < 0 {
		idx += tw.slotCount
	}
	return idx
}

func (tw *timeWheel) scheduleSlot(expireAtNano int64) int64 {
	// 向上取整，确保不早触发。
	targetSlot := (expireAtNano + tw.resolution - 1) / tw.resolution
	currentSlot := now() / tw.resolution

	minSlot := tw.lastProcessedSlot + 1
	if currentSlot > minSlot {
		minSlot = currentSlot
	}
	if targetSlot < minSlot {
		targetSlot = minSlot
	}

	// 限制在固定 24h 窗口内；超过窗口的条目将在扫描时自动续挂。
	maxSlot := minSlot + tw.slotCount - 1
	if targetSlot > maxSlot {
		targetSlot = maxSlot
	}

	return targetSlot
}
