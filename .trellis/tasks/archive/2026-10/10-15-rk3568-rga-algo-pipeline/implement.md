# 算法前处理 RGA 硬件加速与 FrameBroker 零拷贝穿透执行计划 (implement.md)

## Ordered Implementation Checklist

### 阶段 1：算法端接入标准视觉前处理算子 (`FaceDetector`)
- [x] 1.1 修改 `packages/face_recognition/src/detector.hpp`：
  - 更新 `decode_scrfd` 函数签名，追加 `int pad_left = 0, int pad_top = 0` 参数支持居中几何反算。
- [x] 1.2 修改 `packages/face_recognition/src/detector.cpp`：
  - 引入 `<zhulong/algo.hpp>` 工具箱；
  - 重构 `FaceDetector::detect(Session&, const Image&)`：调用 `zhulong::ops::letterbox(image, input_tensor, params)`，移除手写双重 CPU 采样循环与手动通道重排；
  - 完善 `decode_scrfd` 内部反算公式：从 `(cx - bboxes[...])` 扣除 `pad_left` 与 `pad_top` 后再除以 `det_scale`；关键点坐标 `(kx, ky)` 同步扣除偏移量；
  - 维持现有 CPU 桩回退保护逻辑。

### 阶段 2：FrameBroker 零拷贝穿透支持
- [x] 2.1 修改 `native/src/algorithm/ipc/frame_broker.cpp`：
  - 在 `publish_frame(const HardwareFrame& frame)` 中判断 `frame.dma_fd >= 0`；
  - 当为硬件直通帧时，跳过 `std::memcpy` 步骤，直接将 `frame.dma_fd` 赋给待发送的 `dma_fd_to_send`；
  - 维持 `msg.offsets`、`msg.strides`、分辨率及租约代次生命周期的完整性；
  - 针对非硬件帧保留现有的 `std::memcpy` 进槽位兜底逻辑。

### 阶段 3：本地单测与打包验证
- [x] 3.1 运行本地 C++ 引擎与算法运行时测试：
  ```bash
  python3 native/scripts/build.py test
  ```
- [x] 3.2 运行算法包构建与自检：
  ```bash
  python3 packages/face_recognition/scripts/build_package.py --target cpu
  ```
- [x] 3.3 运行全仓库静态检查与 Go 宿主测试：
  ```bash
  make go-check
  go test -race ./internal/...
  ```

---

## Review Gates & Acceptance Verification

1. **代码洁癖审查**：
   - `detector.cpp` 内严禁出现任何未经抽象的单像素 CPU 读写循环；
   - `FrameBroker::publish_frame` 在 `frame.dma_fd >= 0` 时绝对不触发任何 `std::memcpy`。
2. **测试与回归审查**：
   - `python3 native/scripts/build.py test` 必须 100% 通过（包含 `ZhulongAlgoRuntimeTests`, `ZhulongFrameBrokerTests` 等）；
   - 人脸检测输出目标框坐标未发生漂移（满足 `pad_left/pad_top` 偏移还原）。

## Rollback Plan

若修改引起现有单测失败或算法检测精度回退，使用 git 检出还原对应文件：
```bash
git checkout HEAD -- packages/face_recognition/src/detector.cpp packages/face_recognition/src/detector.hpp native/src/algorithm/ipc/frame_broker.cpp
```
