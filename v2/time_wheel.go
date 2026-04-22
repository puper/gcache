package gcache

import "time"

const defaultWheelWindow = 24 * time.Hour

type timeWheel struct {
	slots             []map[uint32]struct{}
	indexToSlot       map[uint32]int64
	resolution        int64
	slotCount         int64
	lastProcessedSlot int64
}

func newTimeWheel(resolution time.Duration) *timeWheel {
	if resolution <= 0 {
		resolution = time.Second
	}

	resNanos := int64(resolution)
	slotCount := int64(defaultWheelWindow / resolution)
	if defaultWheelWindow%resolution != 0 {
		slotCount++
	}
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

func (tw *timeWheel) popExpired(nowNano int64) []uint32 {
	currentSlot := nowNano / tw.resolution
	var expired []uint32

	start := tw.lastProcessedSlot + 1
	if start > currentSlot {
		return nil
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
			if mappedSlot != slotKey {
				continue
			}

			expired = append(expired, idx)
			delete(bucket, idx)
			delete(tw.indexToSlot, idx)
		}

		if len(bucket) == 0 {
			tw.slots[bucketIdx] = nil
		}
	}

	tw.lastProcessedSlot = currentSlot
	return expired
}

func (tw *timeWheel) clear() {
	tw.slots = make([]map[uint32]struct{}, tw.slotCount)
	tw.indexToSlot = make(map[uint32]int64)
	tw.lastProcessedSlot = now() / tw.resolution
}

func (tw *timeWheel) slotIndex(slotKey int64) int64 {
	return slotKey % tw.slotCount
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
