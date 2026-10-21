#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <string>

namespace zhulong::plate {

struct RecognizedPlate {
    std::string plate_number;
    std::string plate_color;
    float score{0.0f};
    int32_t plate_type{0}; // 0=单层, 1=双层, 2=新能源...
};

class PlateRecognizer {
public:
    PlateRecognizer() = default;

    RecognizedPlate recognize(
        zhulong::Session& session,
        const zhulong_frame_view& raw,
        const std::array<float, 4>& box,
        int32_t class_id
    );

    RecognizedPlate recognize_fallback(
        const zhulong_frame_view& raw,
        const std::array<float, 4>& box,
        int32_t class_id
    );
};

} // namespace zhulong::plate
