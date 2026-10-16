# Rockchip MPP 硬件解码节点与端到端零拷贝贯通执行计划 (implement.md)

## Ordered Implementation Checklist

### 阶段 1：MPP 核心契约与动态加载器
- [x] 1.1 创建 `native/src/nodes/decode/mpp_api_types.hpp`：
  - 声明 Rockchip MPP 基础类型、命令字（`MPP_DEC_SET_PARSER_SPLIT_MODE`, `MPP_DEC_SET_EXT_BUF_GROUP`, `MPP_DEC_SET_INFO_CHANGE_READY`）、格式常量（`MPP_FMT_YUV420SP` 等）与 `MppApi` 函数表结构。
- [x] 1.2 创建 `native/src/nodes/decode/mpp_loader.hpp` 与 `mpp_loader.cpp`：
  - 实现 `MppLoader` 符号加载单例，通过 `dlopen` 动态加载 `libmpp.so.1` / `libmpp.so`；
  - 实现硬件支持探针 `is_supported()`（结合 `/dev/mpp_service` 设备节点与符号完整性检查）。

### 阶段 2：MppDecodeNode 硬件解码节点开发
- [x] 2.1 创建 `native/src/nodes/decode/mpp_decoder.hpp` 与 `mpp_decoder.cpp`：
  - 派生 `IDecodeNode`，实现 `MppDecodeNode`；
  - 实现 `send_packet`：支持 H.264/H.265 Annex B 码流包包装借用与 0 字节 EOS drain 信号；
  - 实现 `receive_frame`：实现两段式非阻塞拉取，处理 `info_change` 动态分辨率步长更新与 buffer group 绑定；
  - 提取底层 `MppBuffer` 内核 DMA-BUF 文件描述符回填至 `HardwareFrame::dma_fd`，并绑定 `release_fn`（`mpp_buffer_put`）RAII 回收闭包；
  - 实现 `flush`：清空 DPB 状态。
- [x] 2.2 更新 `native/src/nodes/decode/decoder.hpp` 与静态工厂：
  - 在 `DecoderBackend` 枚举中追加 `MPP_HW = 2`；
  - 重构 `IDecodeNode::create`：在 `AUTO` 或 `MPP_HW` 模式下优先尝试实例化 `MppDecodeNode`；不可用或失败时透明安全回退至 `FFmpegDecodeNode` 软解。

### 阶段 3：单元测试与全链路回归
- [x] 3.1 扩展 `native/tests/decoder_test.cpp`：
  - 验证 `MppDecodeNode::is_supported()` 的优雅降级；
  - 验证工厂方法在 `AUTO` / `FFMPEG_SW` / `MPP_HW` 模式下的行为正确性；
  - 针对 H.264 与 H.265 真实流切片执行解码状态机断言。
- [x] 3.2 运行引擎单元测试与本地自检：
  ```bash
  python3 native/scripts/build.py test
  ```
- [x] 3.3 运行全仓库全量检查：
  ```bash
  make check
  ```

---

## Review Gates & Acceptance Verification

1. **零外部编译期强依赖审查**：
   - 在未安装 Rockchip MPP SDK 的普通 x86_64 / macOS 开发机上执行 `python3 native/scripts/build.py test` 必须 100% 编译通过；
   - 杜绝静态或动态硬链接 `libmpp.so`，防止非 RK 宿主机在启动时发生动态链接加载失败。
2. **生命周期与内存安全审查**：
   - 解码产出的 `HardwareFrame` 必须携带有效的 `release_fn`；在 `HardwareFrame` 析构时必须触发 `mpp_buffer_put`；
   - 外部 `mpp_frame_deinit` 必须在每一帧拉取后立刻执行，防止 MPP 内部帧池死锁挂起。
3. **零拷贝贯通审查**：
   - 当 `MppDecodeNode` 输出 `dma_fd >= 0` 时，送入 `FrameBroker` 能准确触发 Child 1 交付的穿透通路，实现 RTSP ➔ 解码 ➔ Broker ➔ RGA ➔ RKNN 全链路无 `memcpy`。

## Rollback Plan

若改动引起非 RK 平台解码回归或单元测试失败，恢复对应改动文件：
```bash
git checkout HEAD -- native/src/nodes/decode/
```
