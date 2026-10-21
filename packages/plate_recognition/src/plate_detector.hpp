#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <vector>

namespace zhulong::plate {

struct PlateBoxDetection {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 归一化 [0.0, 1.0] */
    float score{0.0f};                                 /**< 检测置信度 */
    int32_t class_id{0};                               /**< 0=单层, 1=双层 */
};

class PlateDetector {
public:
    PlateDetector() = default;

    std::vector<PlateBoxDetection> detect(zhulong::Session& session, const zhulong::Image& image);
    std::vector<PlateBoxDetection> detect_fallback(const zhulong::Image& image);

    void set_confidence_threshold(float conf) noexcept { conf_threshold_ = conf; }
    void set_nms_threshold(float nms) noexcept { nms_threshold_ = nms; }

private:
    float conf_threshold_{0.35f};
    float nms_threshold_{0.45f};
};

} // namespace zhulong::plate
