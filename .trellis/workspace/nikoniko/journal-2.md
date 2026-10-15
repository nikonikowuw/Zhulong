# Journal - nikoniko (Part 2)

> Continuation from `journal-1.md` (archived at ~2000 lines)
> Started: 2026-10-15

---



## Session 39: 支持人员主人脸设置、相册画廊与全屏预览 (10-14-person-primary-face)
<!-- trellis-session: v=2 fp=bd730f31af4d9256 -->

**Date**: 2026-10-15
**Task**: 支持人员主人脸设置、相册画廊与全屏预览 (10-14-person-primary-face)
**Package**: backend
**Branch**: `dev`

### Summary

完成人员主人脸端到端功能支持：新增数据库迁移、自动与手动设主、画廊卡片重构、全屏沉浸式照片预览、复选批量操作及国际化

### Main Changes

- 数据库与后端：新增 000008 迁移脚本，Person 增加 primary_face_id 显式字段，提供 PUT /persons/:id/primary-face 端点及首图自动设主/删除替补逻辑
- 相册画廊：重构 PersonsFacesDialog 为响应式正方形相册网格（Gallery Grid），主人脸高亮角标与悬浮工具栏，内嵌上传卡片与大图预览
- 全屏照片查看：新增 PersonsImagePreviewDialog 沉浸式 Lightbox 查看器，支持原生全屏切换、滚轮缩放与多级放大
- 多选与批量操作：增加独立人脸图列与复选框列，集成 DataTableBulkActions 与 PersonsMultiDeleteDialog 高危批量删除弹窗

### Git Commits

| Hash | Message |
|------|---------|
| `166be4a` | feat(person): support primary face management, gallery grid, and full-screen preview |

### Testing

- [OK] 运行 vitest 无头浏览器测试：28 个测试 100% 通过
- [OK] 运行 make go-check：gofmt / go vet / go test -race / native-engine bridge tests 100% 通过

### Status

[OK] **Completed**

### Next Steps

- 推进 10-14-face-recognition-e2e 剩余人脸特征提取、推理结果出口与前端叠加子任务

---

## Session 40: 实现推理结果跨进程出口 (10-14-inference-result-egress)
<!-- trellis-session: v=2 fp=863d0c91ab54ef82 -->

**Date**: 2026-10-15
**Task**: 实现推理结果跨进程出口 (10-14-inference-result-egress)
**Package**: backend
**Branch**: `dev`

### Summary

打通从 Native Runner 进程到 Go 宿主及前端 SSE 广播的完整推理结果上行回路：增加 IPC 协议结果消息、状态机过滤、Native 路由、C ABI 回调订阅、Go 环形丢最旧背压缓冲、结果反序列化与 SSE 广播，消除结果释放与构造函数零调用状态。

### Main Changes

- Native IPC 与 Runner：在 `ipc_protocol.hpp` 增加 `MSG_RESULT = 7`，Runner 在 `api.process()` 返回后序列化为 JSON 载荷并通过与 FrameBroker 复用的 UDS 连接上报，实现连续空帧静默过滤与退出信标状态机，上报完成后严格在原位 `api.result_release()`（ASan 验证 0 use-after-free）。
- Native Engine 与 C ABI：FrameBroker 处理 `MSG_RESULT` 并经引擎分发至 `AISubscription`；在 `Zhulong/engine.h` 导出 `Zhulong_stream_subscribe_ai_results` / `unsubscribe`，提供同步借用指针回调与排空同步保障。
- Go Engine 与背压：实现 CGO 桥接 `zhulongAIResultCallbackBridge`，以 `cgo.Handle` 传参避免 Go 指针穿越；实现 `AIResultSubscription`，采用固定容量（32）环形切片与 Drop-Oldest 丢最旧背压策略。
- 业务消费与派发：在 `internal/algorithm/result.go` 实现 `ParseInferenceResult`，接线 `NewDetectionResult`、`NewClassificationResult`、`NewPersonStructureResult` 消除死代码；`StreamHub` 自动为 AI 流拉取结果并调用 `EventHub.BroadcastAIResult` 向前端推送 `ai.result` SSE 事件。

### Git Commits

| Hash | Message |
|------|---------|
| `07248f2` | fix(web): avoid synchronous setState inside preview image effect |
| `b49a518` | feat(algo): establish inference result egress channel from runner to Go SSE |

### Testing

- [OK] CTest 15/15 单元测试通过
- [OK] AddressSanitizer (ASan) 15/15 全部通过，0 泄漏，0 use-after-free
- [OK] Go bridge integration tests（真实 RTSP loopback + AI Consumer + Broker UDS + MSG_RESULT 跨语言端到端）100% 通过
- [OK] make go-check（包含 gofmt / go vet / go test -race cmd, internal）全部通过
- [OK] make check（前端 lint、214 个无头浏览器测试、tsc build、native-test、api-docs、go-check）全部通过

### Status

[OK] **Completed**

### Next Steps

- 推进 10-14-face-recognition-e2e 后续子任务（10-14-face-feature-extraction、10-14-gallery-hot-sync、10-14-face-result-frontend）


## Session 41: 完成 native 公开契约层 Doxygen 注释补齐
<!-- trellis-session: v=2 fp=6f2687b558e04820 -->

**Date**: 2026-10-15
**Task**: 完成 native 公开契约层 Doxygen 注释补齐
**Package**: native
**Branch**: `dev`

### Summary

为 native/include/Zhulong/engine.h、frame.hpp 与 native/sdk/include/zhulong/plugin.h、algo.hpp 补齐详尽中文 Doxygen 注释，确立风格基线与术语规范，并通过 norm 预处理剥离检查确保零代码/行为变更。

### Main Changes

- 为 engine.h 补齐错误码、流选项、生命周期及幂等性约束说明
- 为 frame.hpp 补齐 HardwareFrame 各字段语义、NV12 规范与 RAII release_fn 转移契约
- 为 plugin.h 补齐 C ABI 物理隔离要求、帧视图、人脸/人体结构体及 Arena 内存释放约定
- 为 algo.hpp 补齐 C++ SDK 门面、Image 借用生命周期、Session/Model 线程独占语义及单块连续 Arena 内存打包适配器

### Git Commits

| Hash | Message |
|------|---------|
| `2c1eb24` | docs(native): add Doxygen comments for engine C ABI and hardware frame |
| `cfa4cbb` | docs(sdk): add Doxygen comments for plugin C ABI and C++ SDK facade |

### Testing

- [OK] 通过 norm() 预处理代码剥离不变性比对，确保 4 个文件零代码变更
- [OK] make native-build 与 make native-test 全绿 (15/15 通过)
- [OK] make go-check (vet + race + RTSP loopback 集成测试) 全绿通过

### Status

[OK] **Completed**

### Next Steps

- 推进 native 引擎与流水线层注释补齐 (10-14-native-pipeline-docs)
