# 实施计划: 修复原生解码节点合约与 MPP 硬件时序缺陷

## 执行清单

1. [x] Phase 1 需求与设计制定（`prd.md` 与 `design.md`）
2. [x] 步骤 1：修复 `MppDecodeNode` 时间戳判断与背压错误码映射
   - 修改 `native/src/nodes/decode/mpp_decoder.cpp`
   - 将 `out_frame->has_pts = (out_frame->pts != 0)` 改为 `AV_NOPTS_VALUE` 语义；
   - 包含 `<libavutil/error.h>` 并将 `MPP_ERR_BUFFER_FULL` / `MPP_ERR_TIMEOUT` 映射为 `AVERROR(EAGAIN)`；
   - 验证：静态编译通过。
3. [x] 步骤 2：添加 `MppDecodeNode::receive_frame` 异常防泄漏 RAII Guard 与 BufferGroup 清理
   - 修改 `native/src/nodes/decode/mpp_decoder.cpp` 中的 `receive_frame`、`handle_info_change` 与 `destroy_mpp`；
   - 验证：异常路径无泄漏，内存与文件描述符生命周期安全。
4. [x] 步骤 3：增强 `MppLoader` 权限探测与 `FFmpegDecodeNode` 格式兼容
   - 修改 `native/src/nodes/decode/mpp_loader.cpp`（`R_OK | W_OK` 检查）；
   - 修改 `native/src/nodes/decode/ffmpeg_decoder.cpp`（增加 `AV_PIX_FMT_YUVJ420P` 映射与 FrameGuard）；
   - 更新 `native/src/nodes/decode/decoder.hpp` 注释。
5. [x] 步骤 4：在 `native/tests/decoder_test.cpp` 补充单元测试
   - 覆盖 PTS=0 与缺失 PTS（AV_NOPTS_VALUE）的时间戳保真测试；
   - 验证软硬件解码节点在全场景下的契约一致性。
6. [x] 步骤 5：全量构建与测试验证
   - 执行 `make native-test`；
   - 确保无数据竞争、内存泄漏与构建告警（20/20 全部通过）。
