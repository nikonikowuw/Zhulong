# Native Engine Motion Detection Pipeline

## 1. 目标与背景 (Goal & Background)

借鉴 Frigate 的轻量级运动区域检测哲学，在 Zhulong 异构计算平台上实现**流级（Channel-level）前置运动特征提取与算法 Runner 级自动拦截调度**。
解决边缘端（如 RK3568/RK3588、昇腾 Atlas 等）因对空闲画面无脑全帧推理导致的 NPU/GPU 算力浪费，使画面静止时的深度学习推理负载降低至接近 0%，同时保持各业务算法包插件（人脸、车牌、通用检测等）无感透明、零代码修改。

---

## 2. 核心架构设计与分工边界 (Architecture Boundaries)

1. **Native Engine（单次计算、事实提供者）**：
   - 位于 C++ 解码与 FrameBroker 发布管线（`FramePublisher` / `FrameBroker`）。
   - 提取 NV12 Y 平面，通过下采样（320×180 步长采样，适配 L2 Cache，耗时 ~0.25ms）执行轻量级低开销帧差与阈值聚类，提取归一化运动外接框列表（`motion_boxes`）。
   - 全局每流每帧仅计算 1 次（CPU 耗时 <0.5ms），不随挂载算法数量增加而重复计算。
   - 通过 IPC 帧分发协议将运动标志与运动框元数据随帧头广播至各 Runner。
2. **Runner 统一骨架（algo_main.hpp / 运行库，无感自动拦截）**：
   - 各算法包业务代码零感知（保持纯粹的 `detect()` 函数）。
   - 骨架层接收到帧及 `motion_boxes` 后，与该算法配置的 `region_config`（ROI / MASK）执行几何重叠判定。
   - 若全图无运动或运动框与算法 ROI 无交集，直接短路跳过 `detect()` 调用与 NPU 前向计算，立即归还共享内存租约并发送 ACK。
3. **第一期 MVP 决策（用户已确认选择选项 A）**：
   - 第一期聚焦在 **Native 检测引擎 + IPC 协议扩展 + Runner 骨架自动拦截**。
   - 运动判定直接复用各算法任务现有的 `region_config_json`（ROI/MASK）。
   - 不改动 Go 数据库 schema 与前端界面。

---

## 3. 需求清单 (Requirements)

- [x] **R1. Native 运动检测器 (Native MotionDetector)**：
  - 基于 NV12/YUV420P Y 平面下采样的轻量帧差算法（320×180 空间分辨率，单点覆盖 1080p 6×6 像素，远景小目标无感捕获）。
  - 支持全屏光照突变防抖（当运动像素占比超 70% 时判定为环境光突变，重置参考底图并判定为无有效运动）。
  - 输出 `has_motion`（布尔）及最多 4 个归一化运动外接矩形 `[x1, y1, x2, y2]`。
  - **运动保持（hold-off）**：运动停止后继续放行 `MOTION_HOLD_FRAMES` 帧，避免「主体站住不动 ⇒ 目标消失」抖动（逐帧差分是瞬时判据，缺了它会产生持续误报/误清空）。
- [x] **R2. IPC 协议扩展 (IPC Protocol Extension)**：
  - 扩展 `FrameDispatchMsg`：增加 `has_motion` (uint32)、`motion_box_count` (uint32) 以及 `IpcMotionBox motion_boxes[4]` (64 字节)。
  - 维持 8 字节严格自然对齐，结构体大小扩充至 168 字节（168 % 8 == 0）。
- [x] **R3. FrameBroker 发布集成**：
  - `FramePublisher` 在拉取到新帧后运行 `MotionDetector`。
  - `FrameBroker::publish_frame` 携带运动元数据封入 `FrameDispatchMsg` 广播至各 Runner。
- [x] **R4a. Runner 骨架静止拦截**：
  - `algo_main.cpp` 在收到 `FrameDispatchMsg` 后：若 `has_motion == 0`，跳过 `api.process()`。
  - 拦截时**不做** DMA-BUF 映射（静止帧连 mmap/SYNC 都不该发生），立即发送 ACK 归还 slot 租约；若上一帧有目标，补发一次空结果作为消失信标。
  - 兼容遗留 96 字节报文：读不到运动字段时退化为全帧推理，而不是既不推理也不 ACK。
- [ ] **R4b. Runner 骨架 ROI 几何拦截（本迭代显式未实现）**：
  - 设计意图：`has_motion != 0` 时检查运动框与算法配置 ROI 的交集，完全在 ROI 之外则跳过 `api.process()`。
  - **未落地的原因（评审结论）**：Runner 侧没有任何区域配置下发通道（`task_open` 只传 `task_id`）；而 ROI/MASK 策略已由宿主侧 `internal/region.Filter` 在结果侧生效。新增通道是独立跨层特性（Go 监督器 → IPC 消息 → Runner 解析），且判错会**静默丢真目标**，必须带真实画面在板端做误杀率对照后才能上线。
  - 落地前提（验收条件）：① 区域配置下发通道（`MSG_REGION_CONFIG` 一类）；② Runner 侧 `roi_intersects(motion_boxes)` 纯函数 + 单测；③ 板端对照「开启预门禁 / 关闭」的结果差异为零。
  - 在此之前 `motion_boxes` 只作为协议承载，**没有消费者**，文档不得声称已实现 ROI 拦截。
- [x] **R5. 单元测试与验证**：
  - Native 单元测试覆盖全静止帧、局部运动块、全图光照跳变、多框聚类、微噪门槛、hold-off 与 fail-open。
  - FrameBroker IPC 测试与 `static_assert` 对齐验证（含遗留 96 字节前缀兼容、默认参数 fail-open 行为）。
  - Runner 门禁判据由 `native/tests/motion_gate_test.cpp` 直接覆盖（报文解码 + 短路判定），不依赖板端手测。
  - ⚠️ 所有基于 `assert()` 的新测试必须进 CMake 的 `ASSERT_TEST_TARGETS`（`-UNDEBUG`），否则 RelWithDebInfo 下是假绿。
