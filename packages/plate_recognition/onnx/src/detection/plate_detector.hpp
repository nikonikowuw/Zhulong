#pragma once

#include "common/plate_defaults.hpp"
#include "common/plate_semantics.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <atomic>
#include <cstdint>
#include <vector>

namespace zhulong::plate {

/**
 * @brief 单块车牌检测结果
 */
struct PlateBoxDetection {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 归一化 [0.0, 1.0] */
    float score{0.0f};                                 /**< 检测置信度 */
    PlateKind kind{PlateKind::SingleLine};              /**< 层数语义，见 PlateKind */
};

/**
 * @brief YOLOv8 车牌检测器（可被多个 Worker 线程并发调用）
 *
 * ⚠️ 线程安全：本类不持有帧级状态，`detect()` 只读成员阈值，可被多个 Worker
 * 并发调用；每帧的大张量全部在调用栈上分配（见 detect() 注释）。
 * 唯一的可写成员是 `head_mismatch_reported_`（一次性的诊断闩锁，atomic）。
 * 正因为它不可平凡复制（atomic 成员），请不要把检测器塞进任何「先拷贝再改」的
 * 事务式 setter —— 需要改阈值就用 set_*。
 */
class PlateDetector {
public:
    PlateDetector() = default;
    ~PlateDetector() = default;

    PlateDetector(const PlateDetector&) = delete;
    PlateDetector& operator=(const PlateDetector&) = delete;
    PlateDetector(PlateDetector&&) = delete;
    PlateDetector& operator=(PlateDetector&&) = delete;

    std::vector<PlateBoxDetection> detect(zhulong::Session& session, const zhulong::Image& image) const;

    void set_confidence_threshold(float conf) noexcept { conf_threshold_ = conf; }
    void set_nms_threshold(float nms) noexcept { nms_threshold_ = nms; }

    float confidence_threshold() const noexcept { return conf_threshold_; }
    float nms_threshold() const noexcept { return nms_threshold_; }

private:
    float conf_threshold_{kDefaultConfidenceThreshold};
    float nms_threshold_{kDefaultNmsThreshold};
    /** @brief 模型类别数越界的告警只留一次痕：错模型是配置错误，不必逐帧刷屏 */
    mutable std::atomic<bool> head_mismatch_reported_{false};
};

} // namespace zhulong::plate
