/**
 * @file face_types.hpp
 * @brief 人脸识别跨 Stage 通用基础几何类型定义
 */

#pragma once

#include <cstddef>

namespace zhulong::face {

struct Point2f {
    float x{0.0f};
    float y{0.0f};
};

/**
 * @brief 关键点个数（双眼、鼻尖、双嘴角），也是 ArcFace 对齐的输入点数
 *
 * 放在最底层的几何头里而不是 `detector.hpp`：解码（`scrfd_decode.hpp`）与检测
 * 结构体都要用它，而解码必须能脱离检测器头文件单独单测（见 `tests/`）。
 */
inline constexpr size_t kLandmarkCount = 5;

} // namespace zhulong::face
