/**
 * @file yolov8_ppe_detector.hpp
 * @brief YOLOv8 安全帽检测器：letterbox + 后端推理 + 输出布局校验 + 解码
 *
 * 输入尺寸在编译期固定为 640x384（两档权重都是该尺寸）；这里不开放运行期尺寸，
 * 避免「配置改了但权重没换」导致的静默零检出。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <array>
#include <cstdint>
#include <vector>

#include "detection/yolov8_ppe_decode.hpp"

namespace zhulong::ppe {

inline constexpr int kPpeModelWidth = 640;
inline constexpr int kPpeModelHeight = 384;
/** @brief FPN 输出尺度（模型固定三个，输出张量数 = 尺度数 × 2 个头） */
inline constexpr std::array<int, 3> kPpeStrides{8, 16, 32};

/**
 * @brief 每 Worker 独占的前处理缓冲区、尺度视图与解码缓冲
 *
 * 不可拷贝：Tensor 持有像素内存，拷贝一次就是每帧几 MB。由插件按 worker_id 持有
 * `std::unique_ptr`，只在同一 Worker 的推理线程里使用。
 */
class PpeDetectorWorkspace {
public:
    PpeDetectorWorkspace();
    PpeDetectorWorkspace(const PpeDetectorWorkspace&) = delete;
    PpeDetectorWorkspace& operator=(const PpeDetectorWorkspace&) = delete;
    PpeDetectorWorkspace(PpeDetectorWorkspace&&) = delete;
    PpeDetectorWorkspace& operator=(PpeDetectorWorkspace&&) = delete;

    /** @brief letterbox 的目标张量：RKNN 直接写模型输入，ONNX 先写 HWC 中间缓冲 */
    zhulong::Tensor& letterbox_target() noexcept;
    /** @brief 模型实际输入张量（RKNN 为 NHWC UINT8，ONNX 为 NCHW FLOAT32） */
    zhulong::Tensor& model_input() noexcept;
    /** @brief forward 需要的输入列表（单元素） */
    const std::vector<zhulong::Tensor>& model_inputs() const noexcept { return inputs_; }
    /** @brief 本帧的 FPN 尺度视图（跨帧复用容量） */
    std::vector<Yolov8PpeHeadView>& heads() noexcept { return heads_; }
    /** @brief 解码候选/输出/抑制标记缓冲 */
    PpeDecodeBuffers& decode_buffers() noexcept { return decode_; }

    /** @brief 首次几何失配返回 true（每 Worker 只应记一次日志） */
    bool should_report_geometry_mismatch() noexcept;
    /** @brief 首次输出布局失配返回 true（每 Worker 只应记一次日志） */
    bool should_report_layout_mismatch() noexcept;

    const zhulong::Tensor& letterboxed_tensor() const noexcept { return letterboxed_; }

private:
    zhulong::Tensor letterboxed_;         /**< ONNX 档的 HWC UINT8 中间缓冲 */
    std::vector<zhulong::Tensor> inputs_; /**< 单元素，forward 的入参 */
    std::vector<Yolov8PpeHeadView> heads_;
    PpeDecodeBuffers decode_;
    bool geometry_mismatch_reported_{false};
    bool layout_mismatch_reported_{false};
};

/**
 * @brief 安全帽检测器
 *
 * 本身不持有跨帧状态，可被多个 Worker 并发调用；所有可变缓冲与一次性告警位都由
 * 调用方传入的 `PpeDetectorWorkspace`（按 worker 隔离）持有。
 */
class Yolov8PpeDetector {
public:
    /**
     * @brief 单帧检测
     *
     * @param session 该 Worker 的推理会话
     * @param image 原始帧（RGB24 packed）
     * @param score_threshold 置信度阈值
     * @param nms_threshold 每类 NMS IoU 阈值
     * @param workspace 该 Worker 独占的工作区
     * @return 指向 `workspace` 内检测框的借用视图（person / head_helmet /
     *         head_nohelmet，归一化坐标）；下次 detect 或 workspace 析构后失效。
     *         任何失败路径都返回空视图。
     */
    const std::vector<zhulong::Detection>& detect(zhulong::Session& session,
                                                  const zhulong::Image& image,
                                                  float score_threshold,
                                                  float nms_threshold,
                                                  PpeDetectorWorkspace& workspace) const;
};

} // namespace zhulong::ppe
