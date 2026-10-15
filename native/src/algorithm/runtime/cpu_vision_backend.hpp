/**
 * @file cpu_vision_backend.hpp
 * @brief CPU 视觉前处理后端（无硬件依赖的 host 实现）
 *
 * ⚠️ 仅支持 packed RGB24 / BGR24 输入与目标格式；NV12 / YUV420P / RGBA32 / UNKNOWN
 * 等尚未实现的格式返回 NOT_SUPPORTED，且**不得读取源像素**。
 * ⚠️ 几何计算统一复用 compute_letterbox_geometry，与 RGA 后端保持同一套语义。
 */

#pragma once

#include "vision_backend.hpp"

namespace zhulong {

/**
 * @brief CPU 视觉前处理后端（IVisionBackend 的 host 实现）
 */
class CpuVisionBackend : public IVisionBackend {
public:
    /**
     * @brief 图像 Letterbox 预处理（等比缩放、居中补边、颜色转换）
     *
     * 补边值为 114，缩放采用像素中心坐标双线性插值与边界 clamp，直接写入最终 Tensor ROI。
     */
    zhulong_status_t letterbox(const zhulong_frame_view& src,
                               Tensor& dst,
                               LetterboxParams& inout_params) override;

    /**
     * @brief 双线性插值图像缩放；仅支持 packed RGB24/BGR24。
     */
    zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                            zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                            int dst_w, int dst_h, zhulong_pixel_format_t dst_format) override;
};

} // namespace zhulong

