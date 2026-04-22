package gcache

import (
	"hash/maphash"
)

var hashSeed = maphash.MakeSeed()

// hash64 计算 64 位哈希值
func hash64[K comparable](key K) uint64 {
	switch k := any(key).(type) {
	case string:
		return maphash.String(hashSeed, k)
	case bool:
		if k {
			return mix64(1)
		}
		return mix64(0)
	case int:
		return mix64(uint64(k))
	case int32:
		return mix64(uint64(int64(k)))
	case int64:
		return mix64(uint64(k))
	case uint:
		return mix64(uint64(k))
	case uint32:
		return mix64(uint64(k))
	case uint64:
		return mix64(k)
	default:
		// 兜底路径：复杂 key 使用 Comparable 语义哈希，保证等值 key 路由一致。
		return genericHash(key)
	}
}

// mix64 对整数输入做均匀扩散，减少低位偏斜。
func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// genericHash 对复杂 key 使用 maphash.Comparable（兜底路径）。
func genericHash[K comparable](key K) uint64 {
	return maphash.Comparable(hashSeed, key)
}

// shardIndexByHash 通过掩码计算分片索引（分片数必须是 2 的幂）。
func shardIndexByHash(hash uint64, shardMask uint64) uint32 {
	return uint32(hash & shardMask)
}
