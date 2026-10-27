# Native Engine Motion Detection Pipeline 执行规划

> ⚠️ 勾选状态以**代码里真实存在且被测试覆盖**为准。评审实测发现「已勾但未实现」与
>     「已勾但断言被 NDEBUG 编译掉」两类假绿，本文件已按实测结果重写。

## 阶段一：Native 运动检测核心与单元测试
- [x] 编写 `native/src/pipeline/motion_detector.hpp` 与 `motion_detector.cpp`：
  - 基于 NV12/YUV420P Y 平面下采样（320×180）的帧差检测；
  - 全屏光照跳变保护（70%）与宏块 8-连通域聚类（16×9 宏块，栈上定长工作区，零堆分配）；
  - 运动停止后的 hold-off（`MOTION_HOLD_FRAMES`），覆盖「静止主体 ≠ 目标消失」；
  - 构造 `MotionResult`（`has_motion`, `box_count`, `boxes`），外接框直接复用 `ipc::IpcMotionBox`。
- [x] 编写 `native/tests/motion_detector_test.cpp` 单元测试：
  - 首帧初始化、完全静止帧、局部运动块、**多框聚类**（两块互不相邻 ⇒ 2 个外接框）、
    全屏光照跳变、微噪门槛、hold-off 窗口、非法输入 fail-open、`reset()`。
- [x] 测试目标登记进 CMake 的 `ASSERT_TEST_TARGETS`（`-UNDEBUG`）。
  实测 `build/native/host` 是 `RelWithDebInfo`（自带 `-DNDEBUG`），漏登记即「断言全被编译掉、
  测试只打印 PASS」的假绿。该项由 `flags.make` 里是否出现 `-UNDEBUG` 验证。

## 阶段二：IPC 协议扩展与 FrameBroker 集成
- [x] `ipc_protocol.hpp`：新增 `IpcMotionBox` / `FrameMotionInfo`，`FrameDispatchMsg`
  追加 `has_motion` / `motion_box_count` / `motion_boxes[4]`，总大小 96 → **168** 字节；
  `static_assert` 覆盖尺寸、8 字节自然对齐，并用
  `LEGACY_FRAME_DISPATCH_SIZE == offsetof(..., has_motion)` 钉死「追加字段只能落在尾部」。
- [x] `frame_broker.hpp` / `.cpp`：`publish_frame(frame, motion = {})`，默认参数即 fail-open（`has_motion = 1`）。
- [x] 同步更新受影响测试：`frame_broker_test.cpp`（含遗留前缀与默认 fail-open 断言）、`ipc_socket_fd_test.cpp`；
  `rtsp_integration_test.cpp` 使用 `fp.size() < sizeof(...)` 形式，无需改动（已核对）。

## 阶段三：FramePublisher 流水线挂载
- [x] `frame_publisher.hpp` / `.cpp` 持有 `MotionDetector`，在 `publish_loop` 中**节流之后**对帧执行检测；
- [x] 检测失败（格式不支持 / 拿不到像素）一律保持 `FrameMotionInfo` 默认值 ⇒ 全帧推理；
- [x] `stop()` 中 `motion_detector_.reset()`，避免停流后残留底图。

## 阶段四：Runner 统一骨架自动拦截
- [x] `algo_main.cpp`：
  - 报文解码统一走 `sdk/src/internal/motion_gate.hpp` 的 `gate::decode_frame_dispatch`
    （兼容遗留 96 字节报文，读不到运动字段则全帧推理）；
  - `gate::should_skip_inference(has_motion)` 短路 `api.process()`，**短路分支内不做 DMA 映射**；
  - 短路时补发消失信标（若上一帧有目标）并立即 ACK 归还租约。
- [x] `native/tests/motion_gate_test.cpp`：报文往返、遗留前缀 fail-open、截断报文拒绝、短路判据严格性。
- [ ] **ROI 几何预门禁（R4b）——本迭代显式未实现**，见 `prd.md` R4b：
  Runner 侧没有区域配置下发通道，且 ROI/MASK 已由宿主侧 `internal/region.Filter` 在结果侧生效；
  落地需要「Go 监督器 → IPC 消息 → Runner 解析」整条链路 + 板端误杀率对照。

## 阶段五：系统构建、测试与端到端回归
- [x] `make native-build`（含 `libzhulong_algo_sdk.a` 与宿主 `ZhulongEngine`）。
- [x] 主机侧 CTest：`ZhulongMotionDetector`、`ZhulongMotionGate`、`ZhulongFrameBroker`
      （含新增 `test_frame_broker_motion_dispatch`）、`ZhulongIpcSocketFd`、`ZhulongSnapshot`、
      `ZhulongDmaBufAllocator`。
- [ ] **板端未验证**：dma-buf 读取路径（`/dev/mpp_service`）在本机不存在，运动检测的
      DMA 分支一行都没执行到；性能也未实测。上板前不得宣称已达标。
- [ ] `make check` / `go test -race ./...`：本次改动只涉及 C++ 与文档，Go 侧未触及，
      但仍需在收工前跑一遍全栈门禁。
