/**
 * @file detector.hpp
 * @brief 人脸检测、5点关键点定位与人体空间关联模块
 */

#pragma once

#include "aligner.hpp"
#include <zhulong/algo.hpp>
#include <array>
#include <vector>
#include <cstdint>

namespace zhulong::face {

/**
 * @brief 单个检出的人脸与关联人体结构
 */
struct FaceDetection {
    std::array<float, 4> face_box{0.0f, 0.0f, 0.0f, 0.0f};  /**< 归一化人脸框 [x1, y1, x2, y2] ∈ [0, 1] */
    float face_score{0.0f};                                  /**< 人脸检测置信度 */
    std::array<Point2f, 5> landmarks{};                      /**< 归一化 5 点关键点 (双眼、鼻尖、双嘴角) */
    std::array<float, 4> body_box{0.0f, 0.0f, 0.0f, 0.0f};  /**< 关联的人体框 [x1, y1, x2, y2] ∈ [0, 1] */
    float body_score{1.0f};                                  /**< 人体置信度 */
    uint64_t track_id{0};                                    /**< 跟踪目标 ID */
};

class FaceDetector {
public:
    FaceDetector() = default;

    /**
     * @brief 对输入图像执行人脸与人体检测
     * @param image 输入只读帧视图
     * @return 检出的结构化目标列表 (全黑或无目标图像返回空 vector)
     */
    std::vector<FaceDetection> detect(const zhulong::Image& image);
};

} // namespace zhulong::face
