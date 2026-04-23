package gcache

import "time"

// now 获取当前时间戳（纳秒），便于测试时 mock
var now = func() int64 {
	return time.Now().UnixNano()
}

// isExpired 检查是否过期（使用调用方提供的统一时间基准）
func isExpired(expireAt, nowNano int64) bool {
	return expireAt > 0 && nowNano > expireAt
}

// expireTime 计算过期时间戳
func expireTime(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0 // 永不过期
	}
	return now() + int64(ttl)
}
