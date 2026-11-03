/**
 * @file person_detector.hpp
 * @brief 基于 SCRFD 2.5G 的人体检测模块
 */

#pragma once

#include <zhulong/algo.hpp>

#include <array>
#include <vector>

namespace zhulong::crowd {

/**
 * @brief 单个检出人体项（归一化坐标 [0, 1]）
 */
struct DetectedPerson {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 严格归一化框 [x1, y1, x2, y2] ∈ [0, 1] */
    float score{0.0f};                                /**< 检测置信度 */
};

class PersonDetector {
public:
    PersonDetector() = default;
    PersonDetector(int model_width, int model_height, float nms_threshold = 0.45f) noexcept
        : model_width_(model_width), model_height_(model_height), nms_threshold_(nms_threshold) {}

    /**
     * @brief 执行单帧人体检测
     *
     * @param session 当前 Worker 绑定的推理 Session
     * @param image 输入原帧视图
     * @param score_threshold 置信度阈值初筛
     * @return 过滤并经过 NMS 后的归一化人体列表
     */
    std::vector<DetectedPerson> detect(zhulong::Session& session,
                                       const zhulong::Image& image,
                                       float score_threshold) const;

    int model_width() const noexcept { return model_width_; }
    int model_height() const noexcept { return model_height_; }

private:
    int model_width_{640};
    int model_height_{640};
    float nms_threshold_{0.45f};
};

} // namespace zhulong::crowd
