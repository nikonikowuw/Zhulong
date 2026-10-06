# 仓库边界核查

## 已读取证据

- `go.mod` 与 `go version`：Go 1.27.1，宿主 darwin/arm64；Gin 1.12.0、GORM 1.31.2、Fx 1.24.0；版本为本仓库/本环境实测，不代表上游推荐版本。
- `internal/engine/types.go`：StreamStatus 只有 State/Error，没有包计数或最近视频包时间。
- `native/src/nodes/capture/rtsp_input.cpp`：ready 在 av_read_frame 循环前调用；RUNNING 不能当作首包证据。无 consume 的 Probe 在 stream-info 完成后返回，不承诺额外读取一个视频包；FPS 缺失为 0/1。
- `.trellis/spec/native/ingestion-contract.md`：失败流不自动重连，所有引用释放后仍有 8 秒宽限期，期间 reacquire 得到失败实例。业务层简单 Close/Acquire 无法实现 1 秒首次重连。
- `internal/engine/stream.go`：Stream.Close 会先关闭订阅，只有 Preview 消费者允许订阅；不得用虚构 Preview 引用维持健康监测。
- `internal/database/migrations/`：最新迁移 000002，新增摄像机拟使用 000003，实施时再次检查冲突。
- `internal/app/runtime.go`：当前只支持 DB→Native→HTTP 启动及相反停止，没有后台摄像机调度或 SSE 生命周期。
- `internal/app/config.go`：尚无凭据密钥配置。
- 工作树 `native/src/pipeline/engine.hpp` 有用户既存修改，不属于本轮规划修改。

## 实施前门禁

1. 明确 Native 最小能力扩展归属：媒体证据查询与共享失败流重启。不可偷偷修改现有生命周期合同。
2. 轻量 RTSP 客户端依赖需核验源码/固定版本/许可证以及 Describe 是否仅发送预期请求；当前未选定第三方版本。不可复用 FFmpeg Probe 冒充轻量探活。
3. 深度 Probe 的 FPS 缺失策略需与父需求协调；禁止将 0/1 或猜测值标记为实际帧率。

这是规划核查，不是运行测试或目标板验收。
