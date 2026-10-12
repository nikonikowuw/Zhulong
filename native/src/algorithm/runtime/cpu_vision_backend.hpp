#pragma once

#include "vision_backend.hpp"

namespace zhulong {

class CpuVisionBackend : public IVisionBackend {
public:
    zhulong_status_t letterbox(const zhulong_frame_view& src,
                               Tensor& dst,
                               LetterboxParams& inout_params) override;

    zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                            zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                            int dst_w, int dst_h, zhulong_pixel_format_t dst_format) override;
};

} // namespace zhulong
