#pragma once

#include "vision_backend.hpp"

namespace zhulong {

class CpuVisionBackend : public IVisionBackend {
public:
    zhulong_status_t letterbox(const zhulong_frame_view& src,
                               Tensor& dst,
                               LetterboxParams& inout_params) override;

    zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int channels,
                            uint8_t* dst, int dst_w, int dst_h) override;
};

} // namespace zhulong
