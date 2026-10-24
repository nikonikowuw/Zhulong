#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <vector>

namespace zhulong::plate {

/**
 * @brief 检测模型的类别语义（YOLOv8 车牌检测头的 class 索引）
 *
 * 类别数由模型决定（`channels - 4`），本枚举只固定**已约定**的三个语义：
 * 单层 / 双层 / 新能源。模型若只有 2 类，则新能源分支永远不会产出。
 */
enum class PlateLayerClass : int32_t {
    SingleLine = 0, /**< 单层车牌 */
    DoubleLine = 1, /**< 双层车牌（识别前需上下半拼成一行） */
    NewEnergy = 2,  /**< 新能源 8 位车牌（前 2 字仍按单层采样） */
};

/**
 * @brief 单块车牌检测结果
 */
struct PlateBoxDetection {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 归一化 [0.0, 1.0] */
    float score{0.0f};                                 /**< 检测置信度 */
    int32_t class_id{0};                               /**< 模型类别索引，见 PlateLayerClass */
};

/**
 * @brief YOLOv8 车牌检测器（无状态，可被多个 Worker 线程并发调用）
 *
 * ⚠️ 线程安全：本类不持有帧级状态，`detect()` 只读成员阈值，可被多个 Worker
 * 并发调用；每帧的大张量全部在调用栈上分配（见 detect() 注释）。
 */
class PlateDetector {
public:
    PlateDetector() = default;

    std::vector<PlateBoxDetection> detect(zhulong::Session& session, const zhulong::Image& image);

    void set_confidence_threshold(float conf) noexcept { conf_threshold_ = conf; }
    void set_nms_threshold(float nms) noexcept { nms_threshold_ = nms; }

    void set_is_onnx(bool v) noexcept { is_onnx_ = v; }
    [[nodiscard]] bool is_onnx() const noexcept { return is_onnx_; }

private:
    float conf_threshold_{0.35f};
    float nms_threshold_{0.45f};
    bool is_onnx_{false};
};

} // namespace zhulong::plate
