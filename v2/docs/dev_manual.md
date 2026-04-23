# 开发与验证手册

## 本地验证命令
1. 单元测试：`go test ./...`
2. 基准测试（短跑）：`go test -bench=. -benchtime=1s -run=^$ ./...`
3. 32 位交叉编译检查：`GOOS=linux GOARCH=386 go test -c ./...`

## 版本与模块规范
- `v2` 目录按独立 Go Module 维护，根目录必须存在 `go.mod`。
- `module` 路径必须包含主版本后缀：`github.com/puper/gcache/v2`。
- 对外示例导入路径统一使用 `github.com/puper/gcache/v2`。

## 验证重点
- 构建通过且基础功能测试通过。
- TTL 到期清理可触发 `EvictReasonExpired`。
- 回调在锁外触发，无死锁风险。
- benchmark 可覆盖高基数 key、Zipf 热点、并发读写。

## 性能排查建议
- 使用 `-benchmem` 观察分配变化。
- 使用 `-cpuprofile` 定位哈希与过期清理热点。
