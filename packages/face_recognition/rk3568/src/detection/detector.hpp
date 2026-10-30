/**
 * @file detector.hpp
 * @brief 人脸检测、5点关键点定位与人体空间关联模块
 */

#pragma once

#include "common/face_types.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <cstdint>
#include <vector>

namespace zhulong::face {

/** @brief SCRFD 系列默认置信度阈值（人脸/人体共用；低于它不进入解码） */
inline constexpr float kDefaultDetectionScoreThreshold = 0.50f;
/** @brief SCRFD 系列默认 NMS IoU 阈值 */
inline constexpr float kDefaultNmsIouThreshold = 0.40f;
/** @brief 关键点个数（双眼、鼻尖、双嘴角），也是 ArcFace 对齐的输入点数 */
inline constexpr size_t kLandmarkCount = 5;

/**
 * @brief 单个检出的人脸
 *
 * 只保留下游真正消费的字段：像素关键点用于对齐，归一化框与关键点用于结果序列化。
 * 像素坐标与归一化坐标同时保留，避免消费方各自乘一次宽高算出不一致的值。
 */
struct FaceDetection {
    std::array<float, 4> face_box{0.0f, 0.0f, 0.0f, 0.0f};    /**< 归一化人脸框 [x1, y1, x2, y2] ∈ [0, 1] */
    float face_score{0.0f};                                    /**< 人脸检测置信度 */
    std::array<Point2f, kLandmarkCount> landmarks{};           /**< 归一化 5 点关键点 */
    std::array<Point2f, kLandmarkCount> landmarks_pixels{};    /**< 原帧像素坐标 5 点关键点 */
    bool has_landmark_pixels{false};                           /**< landmarks_pixels 是否有效 */
    bool is_real_detection{false};                             /**< 是否来自真实神经网络模型检出 */
};

/**
 * @brief 取检出的 5 点像素坐标
 *
 * 正常情况下解码阶段已经填好像素坐标；只有「只有归一化坐标」的检出才需要乘宽高还原。
 * 把这个判断收在一处，避免每个消费点各写一遍哨兵判据。
 */
inline std::array<Point2f, kLandmarkCount> pixel_landmarks(
    const FaceDetection& detection, int width, int height
) {
    if (detection.has_landmark_pixels) {
        return detection.landmarks_pixels;
    }
    std::array<Point2f, kLandmarkCount> pixels{};
    for (size_t i = 0; i < pixels.size(); ++i) {
        pixels[i].x = detection.landmarks[i].x * static_cast<float>(width);
        pixels[i].y = detection.landmarks[i].y * static_cast<float>(height);
    }
    return pixels;
}

class FaceDetector {
public:
    FaceDetector() = default;

    /**
     * @brief 基于推理会话执行真实 NPU/ONNX 人脸检测 (SCRFD det_500m)
     *
     * `const` 是刻意的：本对象被同一插件的多个 Worker 线程共享，`detect` 不得改动实例状态。
     *
     * @param session 当前 Worker 的检测执行会话
     * @param image 输入只读帧视图
     * @return 检出的结构化目标列表 (会话缺失、预处理失败、输出形状不合法或全黑图时返回空)
     */
    std::vector<FaceDetection> detect(zhulong::Session& session, const zhulong::Image& image) const;

    void set_score_threshold(float threshold) noexcept { score_threshold_ = threshold; }
    void set_nms_threshold(float threshold) noexcept { nms_threshold_ = threshold; }

private:
    float score_threshold_{kDefaultDetectionScoreThreshold};
    float nms_threshold_{kDefaultNmsIouThreshold};
};

/**
 * @brief 单个检出的人体全身结构
 */
struct PersonBodyDetection {
    std::array<float, 4> body_box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 归一化全身预测框 [x1, y1, x2, y2] ∈ [0, 1] */
    float score{0.0f};                                     /**< 人体置信度 */
    bool is_real_detection{false};                         /**< 是否来自真实神经网络模型检出 */
};

class PersonDetector {
public:
    PersonDetector() = default;

    /**
     * @brief 基于推理会话执行真实 NPU/ONNX 人体检测 (SCRFD Person 2.5G)
     *
     * @param session 当前 Worker 的人体检测会话
     * @param image 输入只读帧视图
     * @return 检出的人体目标列表
     */
    std::vector<PersonBodyDetection> detect(zhulong::Session& session, const zhulong::Image& image) const;

    void set_score_threshold(float threshold) noexcept { score_threshold_ = threshold; }
    void set_nms_threshold(float threshold) noexcept { nms_threshold_ = threshold; }

private:
    float score_threshold_{kDefaultDetectionScoreThreshold};
    float nms_threshold_{kDefaultNmsIouThreshold};
};

} // namespace zhulong::face
