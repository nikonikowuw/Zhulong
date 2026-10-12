#pragma once

#include <vector>
#include <cstdint>
#include <zhulong/plugin.h>
#include "tensor.hpp"

namespace zhulong {

struct LetterboxParams {
    int target_width{640};
    int target_height{640};
    zhulong_pixel_format_t target_format{ZHULONG_PIXEL_FORMAT_RGB24};
    float scale{1.0f};
    int pad_left{0};
    int pad_top{0};
};

class IVisionBackend {
public:
    virtual ~IVisionBackend() = default;

    // 图像 Letterbox 预处理 (等比缩放、居中补边、颜色转换)
    virtual zhulong_status_t letterbox(const zhulong_frame_view& src,
                                       Tensor& dst,
                                       LetterboxParams& inout_params) = 0;

    // 双线性插值图像缩放
    virtual zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int channels,
                                    uint8_t* dst, int dst_w, int dst_h) = 0;
};

// 通用高性能向量化非极大值抑制 (NMS)
std::vector<zhulong_bbox> nms(const std::vector<zhulong_bbox>& boxes, float iou_threshold);

} // namespace zhulong
