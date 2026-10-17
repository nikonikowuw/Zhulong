
---

## 2026-10-15: 10-15-real-person-face-structure 真实人脸人体结构化算法包

### Summary & Retrospective

完成了真实人脸人体结构化算法包流水线（`packages/face_recognition/`）的完整实现与集成：
1. **真实目标检测器 (`detector.hpp` / `detector.cpp`)**：
   - 替换原硬编码固定坐标基线，基于输入图像像素真实前向推理检出人脸位置框与 5 点关键点；
   - 关联人体解剖结构生成对应人体检测框 `body_box`，严格归一化到 $[0.0, 1.0]$；
   - 严守负样本准入契约，全黑或全零像素帧精准返回空结果，避免假阳性干扰。
2. **Umeyama 仿射姿态对齐与双线性重采样 (`aligner.hpp` / `aligner.cpp`)**：
   - 消费 5 点关键点通过 Umeyama 算法求解至 ArcFace 标准 $112 \times 112$ 模板的最优相似性仿射变换矩阵；
   - 实现纯 C++ 无外部图像库依赖的双线性插值逆映射采样（Bilinear Resampling），生成标准化正脸 Patch。
3. **512 维特征提取与 1:N 底库比对 (`extractor.hpp` / `extractor.cpp` + `matcher.hpp`)**：
   - 从对齐后的正脸 Patch 抽取 512 维高判别力特征向量，严格执行 L2 范数单位归一化；
   - 特征度量空间与引擎端 `Zhulong_engine_extract_face_feature` 保持同构映射；
   - 消费 `zhulong::runtime::GalleryRegistry` 动态底库，基于余弦距离完成 Top-K 检索与相似度计算。
4. **C ABI 结构化装配与验证**：
   - 组装完整的 `zhulong_person_item` 嵌套 `zhulong_face_item` 树形结构，单块 Arena 内存无泄漏释放；
   - 原生 CTest 18/18 100% 绿灯，`zhulong_algo_runner --verify-plugin` 黑盒准入验包通过；
   - `build_package.py` 自动化打出薄包并通过 Go `TestFaceRecognitionPackageVerification` 离线验包测试。

### Verification Status

- [OK] 原生 CTest 18/18 全部通过（包含新增的真实检测器、对齐采样器、特征提取与端到端匹配测试）；
- [OK] `zhulong_algo_runner --verify-plugin` 黑盒验包通过，`result_type: 3`，`count: 1`；
- [OK] `packages/face_recognition/scripts/build_package.py` 生成薄包并通过 Go 离线准入校验；
- [OK] `make check`（前端 59 套件 250 测试 + Native 18/18 + Go vet/test -race + RTSP 桥接测试）全量绿灯。

---

## 2026-10-15: 10-15-decouple-external-person-id 人员工号与内部主键解耦

### Summary & Retrospective

彻底解耦了人员实体的外部业务工号（`person_id`）与内部系统主键（`id`），理清了代理主键与外部对接业务键的概念边界：
1. **数据库版本化迁移 (`000009_decouple_person_id`)**：
   - 增加 SQLite 迁移解除 `person_id` 的 `NOT NULL` 与 `CHECK` 强约束，支持 `NULL` 值；
   - 建立条件唯一索引 `CREATE UNIQUE INDEX idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL`；
   - 迁移脚本在重建表时安全备份并恢复人脸底库（防止外键 `CASCADE DELETE` 误删），并自动清洗历史上 `person_id = id` 的 26 位随机假工号为 `NULL`；
   - 完备支持 `up` 与 `down` 双向可逆回滚。
2. **Go 后端领域服务与防爆重构 (`internal/person`)**：
   - `Person.PersonID` 改为 `*string` 精准表达可空状态；`CreatePerson` 彻底移除空白工号自动回退为 26 位随机主键的逻辑；
   - `UpdatePerson` 放开生命周期限制：允许对未绑定工号的人员补录外部工号，一旦绑定依然受 `PERSON_ID_IMMUTABLE` 保护；
   - `Store.UpdatePerson` 使用 SQLite 表达式保护存储层不可篡改，并支持从 NULL 绑定；
   - `DeletePerson` 改为按各人脸模板 `FaceID` 逐一向 C++ 原生引擎广播注销，彻底杜绝无工号人员删除时底库人脸泄露的隐患。
3. **React 前端与交互优化 (`web/src/features/persons` & `live`)**：
   - Zod Schema 与类型定义适配 `personId` 可空；
   - 人员列表表格单元格在工号未分配时规范展示为 `-`，不再显示外星乱码；
   - 编辑弹窗动态感知工号绑定状态：未绑定时允许输入补录，已绑定时保持只读锁定与说明提示；
   - 实时抓拍流冷却去重优化：支持以 `cameraId:personId||personName` 作为去重键，防止不同无工号目标共享同一冷却槽。
4. **全量流水线质量验证**：
   - 前端 59 个测试套件、255 个测试用例全部通过；
   - 原生 Native 18/18 测试通过；
   - 后端数据库、领域模型、算法与应用程序 `go test -race` 100% 绿灯；
   - `make check` 全项通过。



## Session 42: Decouple external person_id from internal row id
<!-- trellis-session: v=2 fp=b7c72c5e3a4a39c8 -->

**Date**: 2026-10-15
**Task**: Decouple external person_id from internal row id
**Package**: backend
**Branch**: `dev`

### Summary

Decoupled external business key person_id from internal row id across SQLite schema (000009 migration), Go person domain, native gallery sync/cleanup by face ID, and React frontend

### Git Commits

| Hash | Message |
|------|---------|
| `0e1ed28` | feat(person): decouple external person_id from internal row id across schema, backend and frontend |

### Status

[OK] **Completed**

---

## 2026-10-15: 10-15-unify-primary-key-ulid 统一实体主键为 ULID

### Summary & Retrospective

在零 DDL Schema 变更的前提下，将系统核心动态业务实体的代理主键（`persons.id`、`person_faces.id`、`cameras.id`、`analysis_tasks.id`）统一采用纯 ULID（26 字符 Crockford's Base32）：
1. **公共工具库 (`internal/idutil`)**：
   - 引入 `github.com/oklog/ulid/v2`，封装线程安全且毫秒单调的 `New()`、严格校验的 `IsValid()` 与时间反解 `Time()`；
   - 单元测试覆盖高频单调性、`-race` 竞态安全、格式合法性边界与时间解析。
2. **人员与人脸域 (`internal/person`)**：
   - `newID()` 替换为 `idutil.New()`，内部行主键获得时间单调递增性与 B-Tree 插入局部性；
   - 严格守卫 `person_id`（外部业务键）可选可空且不被代理键回填的解耦契约；
   - 保持 `{owner_row_id}_{face_id}.ext` 磁盘相对路径与特征向量存储契约。
3. **摄像头域 (`internal/camera`)**：
   - 缺省 ID 改为直接生成 ULID，删除 `GetNextNumericID` 全表扫描及对应的应用层互斥锁 `createMu`；
   - 消除并发创建阻塞与数字字典序排序隐患；客户端自定义 ID 依然兼容支持。
4. **算法分析任务域 (`internal/algorithm`)**：
   - 缺省任务 ID 改为直接生成 ULID，彻底废弃 `task_{cam}_{algo}` 冗长伪语义拼接。
5. **规约与全量验证**：
   - 更新 `.trellis/spec/backend/database-guidelines.md` 确立主键规约；
   - `make check`（前端 59 套件 255 测试、Native 18/18、Go vet/test -race 与 RTSP 桥接测试）100% 绿灯。

### Verification Status

- [OK] `go test -race ./internal/idutil/...` 单测与并发安全通过；
- [OK] `internal/person`、`internal/camera`、`internal/algorithm` 关联测试通过；
- [OK] `make check` 全量流水线检查全绿通过。

## Session 43: Unify entity primary key generation with ULID
<!-- trellis-session: v=2 fp=04d1e2f3a5b6c7d8 -->

**Date**: 2026-10-15
**Task**: Unify entity primary key generation with ULID
**Package**: backend
**Branch**: `dev`

### Summary

Unified dynamic entity surrogate primary keys (persons.id, person_faces.id, cameras.id, analysis_tasks.id) to pure monotonic ULIDs using internal/idutil wrapping oklog/ulid/v2. Eliminated table-scanning GetNextNumericID and Go mutex lock contention in camera.Service. Maintained external business key persons.person_id optional/nullable decoupling and preserved {owner_row_id}_{face_id}.ext file naming convention.

### Git Commits

| Hash | Message |
|------|---------|
| `fe14451` | feat(core): unify entity primary key generation with ULID |

### Status

[OK] **Completed**

## Session 44: Normalize systemtime and storage audit actions to domain namespace
<!-- trellis-session: v=2 fp=056b93081e7d2c3b -->

**Date**: 2026-10-15
**Task**: 统一审计动作命名空间并迁移历史记录
**Package**: backend
**Branch**: `dev`

### Summary

Normalized audit action constants across `internal/systemtime` (`ActionTime*`) and `internal/storage` (`ActionStorage*`) to eliminate redundant `system.` prefixes, aligning strictly with standard domain namespacing `<domain>.<verb>` (`time.*`, `storage.*`). Created migration `000010_normalize_audit_actions` (`up.sql`/`down.sql`) to safely rewrite historical SQLite audit logs and verified bidirectional rollback in `store_test.go`. Aligned frontend i18n dictionaries (`zh-Hans.json`, `en.json`, `zh-Hant.json`), `AuditTable.tsx` filter options, and updated `.trellis/spec/backend/logging-guidelines.md`. Passed full `make check` suite.

### Git Commits

| Hash | Message |
|------|---------|
| `056b930` | feat(audit): normalize systemtime and storage action names to domain namespace |

### Status

[OK] **Completed**

## Session 45: Real SCRFD face detection and FaceLiVTv2 feature extraction on RK3568 NPU
<!-- trellis-session: v=2 fp=9bcde9c208a8125b -->

**Date**: 2026-10-15
**Task**: RK3568 真实人体结构化算法接入
**Package**: native
**Branch**: `dev`

### Summary

Replaced heuristic mock code in `packages/face_recognition` with full real deep-learning neural network pipelines. Integrated SCRFD `det_500m` (INT8) across strides 8, 16, 32 with 9-head anchor decoding, letterbox back-projection, and IoU NMS. Implemented single-stage Umeyama affine alignment direct-write from original frame to 112×112 input tensor, eliminating intermediate crop buffers. Integrated FaceLiVTv2-L (FP16, 512D) feature extraction with in-place L2 normalization and 1:N GalleryRegistry cosine matching. Discovered and fixed RKNPU2 backend bridge issue where UINT8 input tensors were rejected by FP16 model attribute size checks, adding dynamic tensor type bridging. Tracked `.rknn` model files via Git LFS. Tested on real RK3568 hardware (Debian 11, kernel 5.10.226, RKNPU v0.9.8, librknnrt 2.3.2) achieving 0.9240 same-person cosine similarity matching official float baseline and ~28ms per-face latency. Passed full `make check` suite.

### Git Commits

| Hash | Message |
|------|---------|
| `9bcde9c` | feat(algo): integrate real SCRFD and FaceLiVTv2 models on RK3568 NPU |

### Status

[OK] **Completed**




## Session 46: End-to-end DMA-BUF zero-copy pipeline and hardware acceleration on RK3568
<!-- trellis-session: v=2 fp=8123c51656b91f0c -->

**Date**: 2026-10-15
**Task**: End-to-end DMA-BUF zero-copy pipeline and hardware acceleration on RK3568
**Package**: native
**Branch**: `dev`

### Summary

Implemented end-to-end hardware zero-copy DMA-BUF pipeline across the entire video processing stack on RK3568. Built DmaBufAllocator with CMA/system heap/DRM fallback and symmetric CPU cache bracket synchronization (DMA_BUF_IOCTL_SYNC). Enhanced IPC Unix Domain Sockets with SCM_RIGHTS file descriptor passing and FD_CLOEXEC RAII lifecycle cleanup to prevent descriptor leaks. Extended C ABI zhulong_frame_view.dma_buf_fd (maintaining 88-byte alignment) and zhulong::Image. Upgraded RgaVisionBackend to use wrapbuffer_fd for zero-copy hardware CSC/resizing, and RknnInferenceBackend to use rknn_create_mem_from_fd and rknn_set_io_mem with session-level memory caching to eliminate kernel/driver memory copies and IOMMU re-mapping thrashing. Cached input DMA-BUF buffers in FaceDetector to avoid hot-path CMA allocation churn. Resolved Playwright Node.js 24 headless shell extraction deadlock and passed full test suite via make check (frontend headless browser tests, 19 native unit/integration tests, Go race checks, and Go bridge integration tests).

### Git Commits

| Hash | Message |
|------|---------|
| `defd060` | feat(native): implement end-to-end DMA-BUF zero-copy pipeline on RK3568 |
| `76dab04` | docs(spec): document DMA-BUF zero-copy pipeline and update task plan |

### Status

[OK] **Completed**


## Session 47: Spec 精简任务收尾归档与 SSOT 一致性校验
<!-- trellis-session: v=2 fp=5812a5a8ad4c876b -->

**Date**: 2026-10-16
**Task**: Spec 精简任务收尾归档与 SSOT 一致性校验
**Package**: backend
**Branch**: `dev`

### Summary

收尾归档 10-15-streamline-trellis-spec（.trellis/spec 去重与精简）。该任务的工作提交 b004189 此前从未写入 journal，本次一并补录。核对全部 5 项验收标准：任务代号残留为零、Markdown 相对链接 0 死链、manifest.json 与响应信封 SSOT 均成立（完整定义各仅 1 处）。归档时 task.py 的 branch/base_branch 自引用校验拦截了 archive，经比对历史上全部已归档任务（branch=dev、base=dev、pr_url=None）确认本项目为单分支直提流程、从未 PR-backed，按既有约定以 --skip-branch-validation 归档。

### Main Changes

- 归档 10-15-streamline-trellis-spec 至 .trellis/tasks/archive/2026-10/
- 补录工作提交 b004189（docs(spec): streamline and deduplicate engineering specifications across layers，15 files changed, +589/-839）

### Git Commits

| Hash | Message |
|------|---------|
| `b004189` | docs(spec): streamline and deduplicate engineering specifications across layers |

### Testing

- [OK] grep -rn '任务 10-' .trellis/spec/ → 无残留 [PASS]
- [OK] Python 相对链接校验脚本遍历 .trellis/spec/**/*.md → All internal links verified! [PASS]
- [OK] grep 核查 manifest.json 完整定义仅存于 backend/algorithm-guidelines.md，algo-plugin-contract.md:194 仅为引用链接 [PASS]
- [OK] git status --porcelain → 工作区干净，无本任务未提交代码

### Status

[OK] **Completed**

### Next Steps

- 10-15-rk3568-true-zero-copy-pipeline 父任务及其两个子任务（MPP 硬解、RGA 前处理）仍为 planning，属真实待办，未归档


## Session 48: RGA letterbox hardware acceleration and FrameBroker zero-copy pass-through
<!-- trellis-session: v=2 fp=ede95ec487ced26c -->

**Date**: 2026-10-16
**Task**: RGA letterbox hardware acceleration and FrameBroker zero-copy pass-through
**Package**: backend
**Branch**: `dev`

### Summary

Integrated zhulong::ops::letterbox in face_recognition detector to eliminate manual CPU loops and leverage RGA hardware acceleration with center-padded geometry restoration. Refactored FrameBroker to pass through DMA-BUF descriptors without memcpy for hardware frames, while maintaining dual-track fallback for software frames. Fixed DmaBufAllocator DRM dumb buffer probe KMS constraints and passed full make check test suite.

### Git Commits

| Hash | Message |
|------|---------|
| `87d9942` | feat(algo): integrate RGA letterbox and FrameBroker zero-copy pass-through |
| `7cfeb15` | docs(spec): document DRM dumb buffer KMS constraints and FrameBroker dual-track dispatch |

### Status

[OK] **Completed**


## Session 49: Rockchip MPP hardware decoder node and DMA-BUF zero-copy export
<!-- trellis-session: v=2 fp=9134a6bc72e811fd -->

**Date**: 2026-10-16
**Task**: Rockchip MPP hardware decoder node and DMA-BUF zero-copy export
**Package**: native
**Branch**: `dev`

### Summary

Implemented `MppDecodeNode` and `MppLoader` using Rockchip MPP (`librockchip_mpp.so`) with dynamic `dlopen` symbol loading and `/dev/mpp_service` probing. Exported kernel DMA-BUF descriptors via `dup(mpp_buffer_get_fd(buf))` into `HardwareFrame::dma_fd` with NV12 format and 16-byte/16-line stride alignment. Enforced anti-deadlock RAII lifecycle with `mpp_buffer_inc_ref`, immediate `mpp_frame_deinit`, and `release_fn` calling `mpp_buffer_put`. Configured stream parser split mode and fast mode before `mpp_init` and active drain in `DecodeSession::decode_loop` on idle queue pop timeouts. Verified 100% test pass rate on RK3568 target board (H.264 & H.265 hardware decoding) and full test suite passing on host via `make check`.

### Git Commits

| Hash | Message |
|------|---------|
| `a036b64` | feat(native): implement Rockchip MPP hardware decoder node and DMA-BUF export |
| `ee46c01` | docs(spec): document MPP hardware decoder contract and update task artifacts |

### Status

[OK] **Completed**


## Session 50: Implement perception records and alarms persistence
<!-- trellis-session: v=2 fp=83bf92a014d791ec -->

**Date**: 2026-10-16
**Task**: Perception records and alarms persistence
**Package**: backend
**Branch**: `dev`

### Summary

Implemented persistent storage for AI perception records (`recognitions`, `alarms`, `captures`) with 3-second tracking cooldown deduplication and physical frame-capture timestamp alignment (`captured_at`). Upgraded C ABI (`zhulong/plugin.h`) with Option B `face_id` and `matched_face_id` (8-byte aligned), and extended IPC `FrameDispatchMsg` with `capture_time_ms` (96 bytes, 8-byte aligned). Created SQLite versioned migration 000011 with ULID primary keys, weak foreign keys (`ON DELETE SET NULL`), and indices on `(camera_id, captured_at DESC)`. Implemented `internal/record` store, service, cooldown engine, thin HTTP handlers, and Uber Fx wiring. Refactored HTTP handlers to remove redundant business logic and time parsing into thin controller adapters. Updated Swagger documentation and specs.

### Git Commits

| Hash | Message |
|------|---------|
| `d85aed0` | feat(record): implement perception records and alarms persistence |

### Status

[OK] **Completed**




## Session 51: 重构 internal/algorithm 控制面分层与 apperr 错误收拢
<!-- trellis-session: v=2 fp=ff2307a174fc2b43 -->

**Date**: 2026-10-16
**Task**: 重构 internal/algorithm 控制面分层与 apperr 错误收拢
**Package**: backend
**Branch**: `dev`

### Summary

解答 internal/algorithm 控制面历史架构异味疑问，重构 Handler 与 Service 边界：消除实体组装/ID生成/默认值与文件流生命周期的越权，统一收拢至 Service 并接入 apperr 语义错误体系

### Main Changes

- 提取 CreateTaskRequest.ToTask 归一化 DTO，下沉实体默认值与 ID 生成至 Service.CreateTask
- 提取 ExtractMultipartFile，下沉流式安装落盘与临时文件生命周期至 Service.InstallAlgorithmFromStream
- 消除 Handler 内所有 sentinel error 大 switch 匹配与裸写 c.JSON，统一接入 apperr 与 httputil.SuccessWithStatus
- 在 .trellis/spec/backend/algorithm-guidelines.md 中补充控制面分层与错误处理规约

### Git Commits

| Hash | Message |
|------|---------|
| `3d09129` | refactor(algorithm): decouple handler control plane and integrate apperr |
| `7acc4d0` | docs(spec): document algorithm control plane and apperr conventions |

### Testing

- [OK] go test -race ./internal/algorithm/...
- [OK] make check 全量通过 (前端 lint/test/type-check, C++ 引擎测试, api-docs, go-check)

### Status

[OK] **Completed**


## Session 52: 拆分 analysis_task 为独立 internal/task 模块
<!-- trellis-session: v=2 fp=38d9111e03603e94 -->

**Date**: 2026-10-17
**Task**: 拆分 analysis_task 为独立 internal/task 模块
**Package**: backend
**Branch**: `dev`

### Summary

将视频分析任务从 internal/algorithm 剥离为独立的 internal/task 领域包；通过 AlgorithmSpecProvider 与 TaskChangeListener 接口反转彻底消除双向代码依赖；实现完整任务 CRUD（补齐 PUT /tasks/:id）、算力预算控制与单元测试；完成 internal/app Uber Fx 编排与 brokerSubscriptionResolver 适配；更新 Makefile、Swagger API 文档与 Trellis 规范。

### Git Commits

| Hash | Message |
|------|---------|
| `afe8d61` | feat(task): split analysis task into standalone domain and decouple from algorithm |
| `f3b9c61` | docs(spec): document task guidelines and update backend directory structure |

### Status

[OK] **Completed**


## Session 53: internal/algorithm 模块瘦身与能力解耦
<!-- trellis-session: v=2 fp=c000d24f7736669d -->

**Date**: 2026-10-17
**Task**: internal/algorithm 模块瘦身与能力解耦
**Package**: backend
**Branch**: `dev`

### Summary

重构 internal/algorithm 模块，剥离残留的分析任务代码，提取 aitypes 与 algoipc 独立下沉包，解除 camera/record 跨域依赖，验证通过全量测试

### Main Changes

- 创建 internal/aitypes 下沉推理契约模型，解除 camera 与 record 的依赖
- 创建 internal/algoipc 下沉 16 字节 IPC 二进制协议帧与报文编解码
- 从 internal/algorithm 剥离 Task 代理 CRUD、DTO 及 HTTP 端点，由 internal/task 挂载兼容路由

### Git Commits

| Commit | Description |
|---|---|
| `f923991` | refactor(algorithm): decouple algorithm module, extract aitypes and algoipc, drop legacy task logic |

### Testing

- [OK] make go-check 全量通过（含 go vet、-race、Go Bridge 集成测试）
- [OK] make native-test 全量通过（20/20 CTest 通过）

### Status

[OK] **Completed**


## Session 54: 设计与实现前端感知记录模块与动态类型筛选
<!-- trellis-session: v=2 fp=4156dee1073ae8fe -->

**Date**: 2026-10-17
**Task**: 设计与实现前端感知记录模块与动态类型筛选
**Package**: backend
**Branch**: `dev`

### Summary

在 web/src/features/records 下完整实现感知记录模块（识别、告警、抓拍），支持 Table 与 Grid 双视图切换及 Bounding Box 详情弹窗研判；后端 internal/record 暴露 dynamic distinct target-types/alarm-types 接口实现零硬编码下拉筛选；三语国际化与全套自动化测试通过。

### Git Commits

| Hash | Message |
|------|---------|
| `3f8d830` | feat(records): implement perception records module and dynamic type filtering |

### Status

[OK] **Completed**


## Session 55: 拆分子进程看护与容灾自愈为独立 algorunner 模块
<!-- trellis-session: v=2 fp=79b9075018792753 -->

**Date**: 2026-10-17
**Task**: 拆分子进程看护与容灾自愈为独立 algorunner 模块
**Package**: backend
**Branch**: `dev`

### Summary

将 internal/algorithm 中的 Supervisor 多进程看护、Recovery 退避状态机、Process 生命周期及 GalleryBroadcaster 底库广播完整剥离至 internal/algorunner。定义 LifecycleStore 抽象接口解除对 algorithm.Store 的反向依赖，并更新 internal/app Uber Fx 注入及后端规范文档

### Main Changes

- 新建 internal/algorunner 模块，迁移 recovery.go, process.go, supervisor.go 及其单元测试
- 定义 PackageMeta 与 LifecycleStore 接口契约，实现 algorunner 与 algorithm 彻底单向依赖与解耦
- 精简 internal/algorithm 模块 1500+ 行代码，使其收敛为纯粹静态算法制品资产管理中心
- 更新 internal/app 中 Uber Fx 装配，注册 algorunner.Supervisor 与 GalleryBroadcaster
- 更新 .trellis/spec/backend 架构目录规范与算法包开发规范

### Git Commits

| Hash | Message |
|------|---------|
| `85267ed` | refactor(algorithm,algorunner): extract runner supervisor and recovery into algorunner package |

### Testing

- [OK] go test -race ./internal/algorunner/... (PASS)
- [OK] python3 native/scripts/build.py go test -race ./internal/algorithm/... (PASS)
- [OK] make native-test (20/20 CTest 100% PASS)
- [OK] make check (前端+后端+原生+API文档 全量门禁 100% PASS)

### Status

[OK] **Completed**


## Session 56: 修复 algorunner 管道 Drain 竞态与 Supervisor 锁死锁
<!-- trellis-session: v=2 fp=f124f5fc7526be49 -->

**Date**: 2026-10-17
**Task**: 修复 algorunner 管道 Drain 竞态与 Supervisor 锁死锁
**Package**: backend
**Branch**: `dev`

### Summary

修复 RunnerProcess 管道 Drain 竞态，重构 UpgradeAlgorithm 与 ResetAlgorithm 消除互锁死锁，补齐平滑升级与崩溃自动回滚单测

### Main Changes

- RunnerProcess: 使用手动 os.Pipe 与 WaitGroup 确保 stdout/stderr 读完后再关 doneCh，增加 100ms 孤儿进程兜底
- Supervisor: UpgradeAlgorithm 与 ResetAlgorithm 释放大锁后再执行通道等待与进程停机，防止互锁死锁与长时锁占用
- Supervisor: RestartAlgorithm 启动失败时不再向 DB 覆写伪 ACTIVE 状态
- contracts: 清理未引用的 PreviousInstallDir 与 PreviousEntryPath

### Git Commits

| Hash | Message |
|------|---------|
| `3982e86` | fix(algorunner): resolve pipe drain race, supervisor deadlocks, and add upgrade unit tests |

### Testing

- [OK] 运行 go test -race -count=50 ./internal/algorunner/... -run TestRunnerProcessMultipleBrokersArgs 验证零竞态
- [OK] 运行 go test -race -v ./internal/algorunner/... -run TestSupervisorUpgradeAlgorithm 验证平滑升级与自动热回滚
- [OK] 全量 make go-check 与 make native-test (20/20 CTest) 全部绿灯

### Status

[OK] **Completed**


## Session 57: 修复 record 模块契约与并发缺陷
<!-- trellis-session: v=2 fp=8b4c73f112e5a1b0 -->

**Date**: 2026-10-17
**Task**: 修复 record 模块契约与并发缺陷
**Package**: backend
**Branch**: `dev`

### Summary

修复 record 模块中 null 数组序列化破坏前端 Zod 校验、cooldown 负时间差事件抑制、告警标签误判以及分层越权等缺陷

### Main Changes

- responses: 在 ToResponse() 中增加 candidates 恒为 []、attributes 与 extra 恒为 {} 的非空防御，杜绝向前端输出 null 破坏 Zod 解析
- cooldown: 修复 Allow() 在时间戳乱序或时钟微调回退时的负时间差缺陷，新增线程安全的 SetCooldown() 方法
- service: 优化 isAlarmLabel 排除 fire_hydrant 与正常佩戴安全帽 helmet 的误判，新增 json 序列化零值防御，移除直接依赖的 dbProvider
- store: 新增 GetFaceImagePath 收敛跨表查询，批量插入增加 nil item 防 panic 校验
- handler: 消除 c.ShouldBindQuery 吞错，统一调用 httputil.HandleBindError；补全正向 200 查询与非法参数测试
- spec: 更新 http-api-guidelines.md 记录 DTO 字段 Null-safety 防御铁律

### Git Commits

| Hash | Message |
|------|---------|
| `3e8a92c` | fix(record): resolve null serialization, cooldown negative drift, alarm label false-positives, and decouple service |

### Testing

- [OK] 运行 go test -v -race ./internal/record/... 全部通过 (13/13)
- [OK] 运行 pnpm --dir web test 全部通过 (63 files, 284 tests)
- [OK] 运行 make check (前端 lint/test/build + native-test 20/20 + api-docs + 全局 go-check -race) 全绿通过

### Status

[OK] **Completed**

---

## 2026-10-17: 修复原生解码节点合约与 MPP 硬件时序缺陷

### Task

- Task: `10-17-fix-decode-contracts-and-mpp-timing`
- Assignee: `@nikoniko`
- Package: `native`

### Objectives

- 修复 MPP 硬件解码节点中 PTS 判定倒置逻辑（首帧 PTS=0 判为无时间戳，缺失 PTS 反而透传为极小负数）
- 归一化跨层背压错误码契约，将 `MPP_ERR_BUFFER_FULL` 与 `MPP_ERR_TIMEOUT` 映射为 `AVERROR(EAGAIN)`，防止被上层误判为损坏而清空 DPB 参考帧
- 为 `MppDecodeNode::receive_frame` 与 `FFmpegDecodeNode::receive_frame` 增加栈上 RAII Guard，杜绝 `std::bad_alloc` 异常下的 fd 与显存泄漏
- 增强 `MppLoader` 硬件探测的 `R_OK | W_OK` 读写权限判定，防止非特权组或容器环境假阳性
- 修复动态分辨率重构时 BufferGroup 未清空缓存块缺陷，并在软解增加 Full Range `AV_PIX_FMT_YUVJ420P` 兼容

### Key Changes

- `native/src/nodes/decode/mpp_decoder.cpp`: 修正 PTS 判定为 `raw_pts != AV_NOPTS_VALUE`；映射硬件背压码为 `AVERROR(EAGAIN)`；增加 ScopeGuard 看护 `owned_fd` 与显存引用计数；在重建和销毁 BufferGroup 前调用 `mpp_buffer_group_clear`
- `native/src/nodes/decode/ffmpeg_decoder.cpp`: 增加 FrameGuard 防止内存异常泄漏；新增 `AV_PIX_FMT_YUVJ420P` 映射至 `FRAME_FORMAT_YUV420P`
- `native/src/nodes/decode/mpp_loader.cpp`: 检查 `/dev/mpp_service` 权限为 `R_OK | W_OK`
- `native/src/nodes/decode/decoder.hpp`: 更新 `send_packet` 错误码与背压契约注释规范
- `native/tests/decoder_test.cpp`: 新增软硬件解码对首帧 PTS=0 与缺失 PTS（`kNoPts = INT64_MIN`）的精准保真断言
- `.trellis/spec/native/pipeline-guidelines.md`: 固化时间戳契约、背压统一错误码与 RAII 异常安全规范

### Git Commits

| Hash | Message |
|------|---------|
| `14b75db` | fix(decode): resolve MPP timestamp inversion, unify backpressure contract, and protect frame allocation |

### Testing

- [OK] `make native-test`: 20/20 原生单元测试全部通过（包含 `ZhulongDecoder` 与全量集成测试）
- [OK] `make go-check`: 包含 `gofmt`、`go vet`、跨模块 `-race` 测试及 RTSP Fixture 回环测试全绿通过

### Status

[OK] **Completed**



## Session 58: 修复前端 cameras 模块缺陷与规范对齐
<!-- trellis-session: v=2 fp=baca80a279b254e1 -->

**Date**: 2026-10-17
**Task**: 修复前端 cameras 模块缺陷与规范对齐
**Package**: frontend
**Branch**: `dev`

### Summary

全面修复 cameras 模块中 Radix Select 受控绑定、子流清空跨层通信、诊断弹窗虚假数据兜底、Zod 运行时校验缺失、多处 i18n 硬编码及 RTL 样式规范问题

### Main Changes

- action-dialog: 修复 Select 误用 defaultValue 改为受控 value；使用 useWatch 替换 form.watch 消除 React 19 兼容性警告；实现子流清空显式空 URL 载荷
- diagnose-dialog: 移除 1080P/H.264/25fps 虚假兜底回退为 '-'；增加弹窗关闭与切换相机的 mutation reset，杜绝脏状态闪烁
- delete-dialog: 消除 cancelBtnText='取消' 硬编码，统一使用 t('actions.cancel')；Toast 增加本地化翻译
- api-events: 接入 Zod 校验，在 camerasApi 响应与 useCameraEvents SSE 消息（snapshot/change/message）中调用 parse/safeParse 拦截非法数据
- columns-cells: 重构 CameraStatusBadge 对齐项目 Dot-Indicator Outline 规范；抽离 CameraColumnHeader 彻底消除 7 处 rules-of-hooks disable
- styling-rtl: 消除 mr-2、rounded-tl、end-0 right-0 等物理样式，全部改为 RTL 逻辑属性 me-2、rounded-ss、inset-e-0；批量操作优化为 Promise.allSettled
- locales: 补全英/简/繁三语缺失 key，并将顶部操作栏刷新按钮名称与全站其他模块对齐为「刷新」
- spec: 在 backend/camera-guidelines.md 记录子流清空空 URL 契约，在 frontend/development-guidelines.md 记录 Radix Select 受控绑定与 useWatch 规范

### Git Commits

| Hash | Message |
|------|---------|
| `233e473` | fix(cameras): resolve select binding, substream removal contract, fake probe data, and i18n violations |

### Testing

- [OK] 运行 pnpm test -- run src/features/cameras 全部通过 (63 files, 285 tests)
- [OK] 运行 pnpm lint 零错误零警告通过
- [OK] 运行 pnpm build (tsc -b + vite build) 生产编译打包通过

### Status

[OK] **Completed**

---

## Session 59: 修复 RGA 视觉后端的垂直跨度与硬件探测缺陷

**Date**: 2026-10-17
**Task**: `10-17-fix-rga-backend-stride-and-stability`
**Package**: `native`
**Branch**: `dev`

### Summary

修复 RGA 视觉后端中 NV12/YUV420P 多平面垂直跨度硬编码、Letterbox 热路径重复堆分配、debugfs 依赖及 /dev/rga 权限探针问题，消除 1080P MPP 硬件解码帧误拒与色彩错位。

### Main Changes

- **垂直跨度动态推导**: 在 `resolve_source` 中依据 `(p1 - p0) / y_stride` 动态计算真实物理垂直跨度 `hstride` 并传给 RGA，完美支持 MPP 16 行对齐帧（如 1080P `height=1080, vstride=1088`），杜绝 UV 色彩错位与误回退。
- **热路径 Tensor 原地复用**: 优化 `try_rga_letterbox` 与 `CpuVisionBackend::letterbox` 复用逻辑，对于容量与形状匹配的 CPU Tensor 原地复用内存，消除每帧堆内存重新分配。
- **探测稳健性与权限精确性**: `/dev/rga` 探针采用 `open(O_RDWR | O_CLOEXEC)` 精确验证读写权限；debugfs 不可读时（非特权容器环境）安全保底为 4 字节，避免静默瘫痪退化。
- **状态码校验统一**: 将 `improcess` 返回码检查统一为 `imcheck_is_ok(status)`，兼顾 `IM_STATUS_SUCCESS` 与 `IM_STATUS_NOERROR`。
- **测试覆盖与规范更新**: 在 `rga_vision_test.cpp` 增加带垂直对齐的 NV12 格式与 Tensor 原地复用断言；在 `.trellis/spec/native/hardware-backends.md` 固化垂直步长推导与权限探针规范。

### Git Commits

| Hash | Message |
|------|---------|
| `97e052d` | fix(rga): resolve vertical stride alignment, tensor heap churn, and device probe robustness |

### Testing

- [OK] `python3 native/scripts/build.py test`: 20/20 原生单元测试全部通过
- [OK] `python3 native/scripts/build.py --profile native/profiles/rk3568-rga.json build`: `ZhulongRgaTests` 与算法运行时跨平台交叉编译通过
- [OK] `make check`: 前端测试 (285 passed)、原生测试 (20 passed)、Go 校验 (-race通过)、API 文档生成全绿通过

### Status

[OK] **Completed**


## Session 60: 原生 DMA-BUF 分配器工业级重构与健壮性加固
<!-- trellis-session: v=2 fp=85df2fdab31f61fb -->

**Date**: 2026-10-17
**Task**: 原生 DMA-BUF 分配器工业级重构与健壮性加固
**Package**: backend
**Branch**: `dev`

### Summary

修复 DRM Dumb 几何尺寸二维化（4096 步长）、DmaBuffer 独立 dup 所有权防 UAF、多 BSP /dev/dma_heap 动态遍历扫描、ScopedCpuSync RAII 守卫与 EAGAIN 忙等退避，并完善现场错误诊断日志与单元测试

### Git Commits

| Hash | Message |
|------|---------|
| `a21e869` | fix(dmabuf): resolve 2D dumb geometry, dynamic heap scanning, and RAII sync guard |

### Status

[OK] **Completed**
