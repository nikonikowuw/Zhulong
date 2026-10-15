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


## Session 42: 完成 native 引擎与流水线层 Doxygen 注释补齐
<!-- trellis-session: v=2 fp=8a7c2d9e1f3b4c50 -->

**Date**: 2026-10-15
**Task**: 完成 native 引擎与流水线层 Doxygen 注释补齐
**Package**: native
**Branch**: `dev`

### Summary

为 native/src/abi、native/src/pipeline、native/src/nodes 15 个 C++ 源文件与头文件补齐详尽中文 Doxygen 注释，覆盖五步优雅停机时序、锁外汇合禁令、按需解码会话生命周期、有界队列关键帧自愈重同步、时间戳保真以及异常防爆门机制，并通过 norm 预处理剥离检查确保零代码/行为变更。

### Main Changes

- 为 native/src/abi/engine.cpp 补齐 C ABI 异常防爆门、句柄转换所有权、同步排空及回调上下文控制拒绝语义
- 为 native/src/nodes/capture/rtsp_input.{hpp,cpp} 补齐 RTSP 异步建连/超时策略、视频元数据透传及 extradata 借用生命周期
- 为 native/src/nodes/decode/{decoder.hpp,ffmpeg_decoder.{hpp,cpp}} 补齐 IDecodeNode 契约、EAGAIN 排空重试循环、解码异常防爆门及硬件帧 release_fn 闭包回收
- 为 native/src/pipeline/bounded_queue.hpp 补齐三种溢出策略、取消状态不可复用约束及锁外执行回调原则
- 为 native/src/pipeline/{subscription,frame_publisher}.{hpp,cpp} 补齐订阅异常逐订阅者隔离、同步排空语义以及发布侧节流口径
- 为 native/src/pipeline/{stream,engine}.{hpp,cpp} 补齐按需 DecodeSession 三态生命周期、有界关键帧重同步、五步优雅停机及锁外 join 禁令

### Git Commits

| Hash | Message |
|------|---------|
| `8f5beb7` | docs(nodes): add Doxygen comments for capture nodes, decode nodes, and ABI boundary |
| `ff463d3` | docs(pipeline): add Doxygen comments for engine orchestration, stream pipeline, and bounded queues |

### Testing

- [OK] 通过 norm() 预处理代码剥离不变性比对，确保 15 个文件零代码变更
- [OK] make native-build 与 make native-test 全绿 (15/15 通过)
- [OK] make go-check (vet + race + RTSP loopback 集成测试) 全绿通过

### Status

[OK] **Completed**

### Next Steps

- 推进 native 算法子系统注释补齐 (10-14-native-algorithm-docs)


## Session 43: 完成 native 算法子系统注释补齐与父任务收尾
<!-- trellis-session: v=2 fp=3c9e5a71d84b620f -->

**Date**: 2026-10-15
**Task**: 完成 native 算法子系统注释补齐与父任务收尾
**Package**: native
**Branch**: `dev`

### Summary

为 native/src/algorithm/{ipc,runner,runtime} 与 native/src/backends/{rknn,rga} 共 28 个文件补齐中文 Doxygen 注释，覆盖 IPC 字节级对齐、SHM 租约状态机与代次防 ABA、Runner 退出码表、RKNN SDK 缺陷规避与 RGA 驱动级禁令。随后完成父任务 10-14-native-core-code-docs 的跨层术语一致性终审与全栈门禁。

### Main Changes

- IPC（8 文件）：补齐 `ShmHeader` 64 字节布局、IPC 报文 8 字节自然对齐与 static_assert 意图、64KB payload 跨语言门禁、四态 socket 状态机与半包熔断、槽位 `FREE→WRITING→LEASED→FREE` 状态机、代次防 ABA 静默丢弃、慢消费者 Drop-Latest 与崩溃自愈、多平面逐行安全复制
- Runner（2 文件）：补齐退出码表（0~8）与看门狗依赖说明、`parse_stream_id_from_socket` 唯一路径依赖约定、多路 `--broker` 的 D6 语义、json_quote 控制字符转义规约
- Runtime（11 文件）：补齐 Tensor 形状/溢出异常语义、`IInferenceBackend`/`IVisionBackend` 抽象边界、LetterboxParams 的 in-out 语义、编译期后端选择后果、CpuVisionBackend stride 契约与补边值 114、letterbox 几何单源共用
- Backends（7 文件）：补齐 RKNN `RKNN_QUERY_MEM_SIZE` 上下文污染缺陷规避（R10）、`dup_mutex` 串行化与失败路径防 double-destroy（R11）、`rknn_input` 无 stride 且**不得声称零拷贝**（R13）、`librknnrt.so` 链接约束（R14）、RGA 目标指针绝不偏移及越界死锁症状（R12）、运行期对齐值探测严禁硬编码

### Git Commits

| Hash | Message |
|------|---------|
| `03ece54` | docs(algo-ipc): add Doxygen comments for IPC protocol, SHM pool, and frame broker |
| `93ee8ac` | docs(algo-runtime): add Doxygen comments for runner and runtime HAL |
| `19526ea` | docs(algo-backends): add Doxygen comments for RKNN and RGA vendor backends |
| `98e9595` | docs(sdk): correct TaskContext terminology to 任务状态 per glossary |

### Testing

- [OK] 通过 norm() 预处理代码剥离不变性比对，确保 28 个文件零代码变更（过程中捕获并修正 3 处误伤：尾逗号与两处换行合并）
- [OK] make native-test 全绿 (15/15)；rm -rf build/native/host 后全新构建验证
- [OK] make check 全栈门禁全绿（前端 + native 15/15 + api-docs + go-check）
- [OK] 跨层术语终审：发现并修正 C1 遗留 7 处「任务上下文」别名，复查零命中
- [OK] R1~R20 风险点终审：20/20 全部有落点

### Status

[OK] **Completed**（子任务与父任务均已归档）

### Next Steps

- 推进人脸识别端到端子任务（10-14-face-feature-extraction 等）


## Session 44: 完成人脸特征提取闭环 (10-14-face-feature-extraction)
<!-- trellis-session: v=2 fp=8a29e4d1f03c5b1e -->

**Date**: 2026-10-15
**Task**: 实现人脸特征提取闭环（10-14-face-feature-extraction）
**Package**: backend, native
**Branch**: `dev`

### Summary

基于「方案 2（Engine 独立提取与底库持久化）」完成了人脸照片录入的一站式特征提取、格式验证与向量持久化闭环。新增 Native Engine C ABI 单次提取接口 `Zhulong_engine_extract_face_feature`，在 Go `internal/engine` 中安全封装 CGO 调用（严格遵从 `runtime.KeepAlive` 与零拷贝/浅视图规约），并在 `internal/person` 中通过接口 `FaceFeatureExtractor` 依赖注入，将原来 2048 字节全零占位符替换为真实 512 维 float32 特征向量持久化至 SQLite `person_faces.feature_vector`，同时在 `GetGallerySyncData` 中向下游下发真实特征与图片路径，支持 Runner 毫秒级瞬时冷启。

### Main Changes

- **Native C ABI (`native/`)**：
  - 在 `native/include/Zhulong/engine.h` 声明 `Zhulong_engine_extract_face_feature`，支持 RGB24/BGR24 格式入参校验、全黑/零像素人脸检测判断（返回 `Zhulong_ERR_NOT_FOUND`）、并写入 512 维 L2 归一化特征向量。
  - 在 `native/src/abi/engine.cpp` 实现提取逻辑，全局 `try/catch` 隔离所有 C++ 异常，杜绝异常穿越 ABI。
  - 新增 Native 单元测试 `native/tests/face_feature_extract_test.cpp` 并集成至 CMake CTest。
- **Go Engine 桥接 (`internal/engine/`)**：
  - 扩展 `(*Engine).ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)`，内置标准 `image/jpeg` 与 `image/png` 解码并打包为连续 packed RGB24 内存，通过 CGO 传参并使用 `runtime.KeepAlive` 保证调用期不被 GC 回收。
  - 新增 `internal/engine/engine_face_test.go` 针对提取 API 开展竞态与功能测试。
- **错误码与多语言 (`internal/httputil/`)**：
  - 在 `locale.go` 注册 `NO_FACE_DETECTED`（未检测到有效人脸）与 `FEATURE_EXTRACT_FAILED`（特征提取失败）的三语本地化文案。
- **Person 领域与底库持久化 (`internal/person/`)**：
  - 抽象 `FaceFeatureExtractor` 接口，解耦 CGO 依赖，通过 Uber Fx 在 `internal/app` 将 `*engine.Engine` 注入 `person.Service`。
  - 改造 `AddFaceWithReader`：提取特征成功后才落盘图片与持久化 2048 字节 Little-Endian 向量至 SQLite；提取失败立即返回 `NO_FACE_DETECTED` 且不残留磁盘文件与数据库脏记录。
  - 实现 `float32ToBytes` 工具函数，并让 `GetGallerySyncData` 下发完整的 `ImagePath` 与真实特征切片。
  - 扩充 `internal/person/service_test.go`，全面覆盖提取成功、未检测到人脸、维度不符等测试用例。
- **契约规范与任务归档**：
  - 更新 `.trellis/spec/native/cgo-contract.md` 补全单次离线特征提取 C ABI 与 CGO 安全规范；
  - 顺利通过 `make check`（前端全部测试 + Native 16/16 CTest + api-docs + Go vet & race & RTSP bridge 测试）；
  - 归档子任务 `10-14-face-feature-extraction`。

### Git Commits

| Hash | Message |
|------|---------|
| `1ce8766` | feat(person): implement one-shot face feature extraction and persistence |
| `0e47087` | chore(task): archive 10-14-face-feature-extraction |

### Testing

- [OK] `make native-test`：16/16 CTest 全绿通过
- [OK] `python3 native/scripts/build.py go test -race ./internal/engine -run TestExtractFaceFeature`：通过
- [OK] `go test -race ./internal/person/...`：通过
- [OK] `make go-check`：gofmt、go vet、全包 -race 测试以及 RTSP 真实桥接集成测试全绿通过
- [OK] `make check`：全栈验证全量通过

### Status

[OK] **Completed**（子任务已归档）

### Next Steps

- 推进人脸识别端到端其余子任务：
  - `10-14-gallery-hot-sync`（底库热更新通道）
  - `10-14-face-algo-package`（人脸识别算法包构建）
  - `10-14-face-result-frontend`（前端叠加与记录）

---

## 2026-10-15: 实现底库热更新通道 (10-14-gallery-hot-sync)

### Task Information

- **Task**: 10-14-gallery-hot-sync (实现底库热更新通道)
- **Parent**: 10-14-face-recognition-e2e
- **Package**: backend, native
- **Type**: feature / native IPC & go integration

### Implementation Details

- **Native IPC 协议与下行广播通路**：
  - 在 `native/src/algorithm/ipc/ipc_protocol.hpp` 中新增 `MSG_GALLERY_SYNC` (10), `MSG_GALLERY_UPSERT` (11), `MSG_GALLERY_DELETE` (12), `MSG_GALLERY_ACK` (13)；
  - 在 `FrameBroker` 与 `Engine` 中实现 `broadcast_message` / `broadcast_ai_message`，通过复用既有 `/tmp/zhulong_broker_<stream_id>.sock` 下发下行控制指令，导出 C ABI `Zhulong_engine_broadcast_ai_message`；
- **Go Engine 与分片广播机制**：
  - 在 `internal/engine` 中封装 `BroadcastAIMessage`，支持向各流的 FrameBroker 安全广播报文；
  - 在 `internal/algorithm/process.go` 中实现 `CreateGallerySyncShards`（基于 10 项/片切分，严格控制单片在 40~50KB，规避 64KB 单包 IPC 上限）；
  - 实现 `GalleryBroadcaster` 统一提供 `BroadcastSync`、`BroadcastUpsert`、`BroadcastDelete`；
  - 修复 `Supervisor.StartAlgorithm` 中在持有 `s.mu.Lock()` 时调用 `triggerGallerySyncAsync()` 导致的 `sync.RWMutex` 自重入死锁问题；
- **Runner 进程内底库索引与解析**：
  - 编写 `GalleryIndex` 线程安全本地索引，支持快照隔离 `snapshot()`；
  - 在 `runner.cpp` 中接入 `MSG_GALLERY_SYNC`（分片收集与超时窗口组装）、`MSG_GALLERY_UPSERT` 与 `MSG_GALLERY_DELETE`，组装完成后回传 `MSG_GALLERY_ACK`；
- **Person 领域与 App 接线**：
  - 在 `internal/person/service.go` 中接入 `GalleryNotifier`，在人脸增删与人员修改时即时增量下发更新；
  - 在 `internal/app` 中完成 Uber Fx 绑定与生命周期闭环；
- **契约规范与全量门禁**：
  - 更新 `.trellis/spec/native/algo-plugin-contract.md` §7.3，登记底库热更新链路已实现与分片规约；
  - `make check`（前端全部测试 + Native 17/17 CTest + api-docs + Go vet & race & RTSP bridge 测试）全量通过。

### Testing

- [OK] `make native-test`：17/17 CTest 全绿通过
- [OK] `python3 native/scripts/build.py go test -race ./internal/algorithm`：通过
- [OK] `python3 native/scripts/build.py go test -race ./internal/person`：通过
- [OK] `make go-check`：全包 -race、vet、gofmt 与 RTSP 真实桥接测试全绿通过
- [OK] `make check`：全栈测试通过

### Status

[OK] **Completed**（已归档）

### Next Steps

- 推进人脸识别端到端后继子任务：
  - `10-14-face-algo-package`（实现人脸识别算法包构建与 1:N 检索）
  - `10-14-face-result-frontend`（前端叠加与记录展示）

---

## 2026-10-15: 实现人脸识别算法包 (10-14-face-algo-package)

### Task Information

- **Task**: 10-14-face-algo-package (实现人脸识别算法包)
- **Parent**: 10-14-face-recognition-e2e
- **Package**: native, backend
- **Type**: feature / algorithm package & plugin

### Implementation Details

- **进程内跨库底库共享注册表 (`GalleryRegistry`)**：
  - 在 `native/src/algorithm/runtime/gallery_registry.{hpp,cpp}` 中实现进程内跨库单例，集成至 `ZhulongAlgoRuntime`；
  - 采用写时复制读写隔离（`reset`, `upsert`, `remove` 原子重构 `snapshot_`），推理热路径通过 `snapshot()` 获取不可变智能指针，零锁争用；
  - 联动 `native/src/algorithm/runner/gallery_index.hpp`，下行 `MSG_GALLERY_*` 消息自动同步至全局 `GalleryRegistry` 单例；
- **人脸识别插件 (`FaceRecognitionPlugin`)**：
  - 在 `packages/face_recognition/src/` 中实现：
    - `matcher.{hpp,cpp}`：512 维特征向量余弦相似度计算与 Top-K 堆排序；
    - `aligner.{hpp,cpp}`：ArcFace 5 点参考模板几何对齐（Umeyama 相似变换矩阵求解）；
    - `face_recognition.cpp`：实现纯 C ABI 导出插件，支持通过 `init(config_json)` 动态配置 `threshold` 与 `top_k`；输出退化结构化人体项（`track_id = 0`, `body_score = 1.0f`, `body_box = face_box`）；
- **算法包源码工程与目标芯片物理隔离**：
  - 建立 `packages/face_recognition/` 结构，按 `targets/cpu/` 与 `targets/rk3568/` 严格隔离芯片专属 `manifest.json` 与权重模型；
  - 编写 `packages/face_recognition/scripts/build_package.py` 自动化组装与黑盒校验脚本，一键生成单一目标扁平 `.tar.gz` 算法包；
- **测试与验证门禁**：
  - `native/tests/face_recognition_plugin_test.cpp`：覆盖空底库验包、正样本命中（>0.999）、负样本阈值拒绝与全黑图像无脸负样本测试；挂载入 CTest（18/18 全绿）；
  - `internal/algorithm/face_recognition_pkg_test.go`：验证打出的算法包通过 Go 侧 `verifier.StageAndVerify` 静态校验与动态 Runner 执行门禁；
  - 更新 `.trellis/spec/native/algo-plugin-contract.md` §7.4 登记 `GalleryRegistry` 规约。

### Testing

- [OK] `make native-test`：18/18 CTest 全绿通过
- [OK] `go test -race ./internal/algorithm -run TestFaceRecognitionPackageVerification`：通过
- [OK] `make go-check`：全包 -race、vet、gofmt 与 RTSP 真实桥接测试全绿通过
- [OK] `make check`：全栈测试通过

### Status

[OK] **Completed**

### Next Steps

- 推进前端识别结果展示子任务：
  - `10-14-face-result-frontend`（实现识别结果前端叠加与记录）




