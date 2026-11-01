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
/** @brief 默认通用检测输入分辨率（1:1 方形，用于底库静态单图特征提取等） */
inline constexpr int kDefaultModelWidth = 640;
inline constexpr int kDefaultModelHeight = 640;
/** @brief 监控视频流优化输入分辨率（16:9 CCTV 场景，相比 640×640 减少 ~40% 计算量并保证 FPN 步长整除） */
inline constexpr int kCctvStreamModelWidth = 640;
inline constexpr int kCctvStreamModelHeight = 384;
/** @brief 人脸检测的 FPN 步长粒度（strides 8/16/32），输入尺寸必须是它的整数倍 */
inline constexpr int kFaceInputGranularity = 32;
/** @brief 人体检测的 FPN 步长粒度（strides 8/16/32/64/128），输入尺寸必须是它的整数倍 */
inline constexpr int kPersonInputGranularity = 128;

/**
 * @brief 本档位后端是否只接受模型自带的输入尺寸（静态 shape 模型）
 *
 * rk3568 的 `.rknn` 是 `static_shape [1,3,640,640]`（`dynamic_shapes` 为空）；RKNN 后端
 * 按 `element_count` 校验输入，尺寸不符时 `forward` 直接返回空 —— 故障表现是「检测恒为 0」
 * 且没有任何错误码。onnx 档位的 det_500m / scrfd_person_2.5g 输入空间维是动态轴
 * （`[1,3,?,?]`），因此可以按流调整分辨率（如 16:9 的 640×384）。
 *
 * ⚠️ 由 SDK 前缀的 `INTERFACE_COMPILE_DEFINITIONS` 区分（`ZHULONG_INFERENCE_RKNN` /
 *    `_ONNX`），与 `zhulong::TARGET_MODEL_EXT` 用同一组定义，不按架构宏猜。
 */
#if defined(ZHULONG_INFERENCE_RKNN)
inline constexpr bool kDetectionInputShapeIsStatic = true;
#else
inline constexpr bool kDetectionInputShapeIsStatic = false;
#endif

/**
 * @brief 判断检测输入分辨率是否与 FPN 步长表兼容
 *
 * 不兼容时解码网格与模型输出必然错位，而 `scrfd_outputs_match` 只能看到「张量个数/element_count
 * 不符」并返回空结果 —— 那就是一次静默的零检出。因此校验必须发生在设置阶段（fail fast）。
 */
inline bool is_supported_detection_input(int width, int height, int granularity) noexcept {
    return width > 0 && height > 0 && granularity > 0 &&
           width % granularity == 0 && height % granularity == 0;
}
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
    FaceDetector(int model_width, int model_height) noexcept
        : model_width_(model_width), model_height_(model_height) {}

    int model_width() const noexcept { return model_width_; }
    int model_height() const noexcept { return model_height_; }

    /**
     * @brief 设置人脸检测输入分辨率
     * @return false 表示该分辨率不可用（未整除 FPN 粒度，或本档位模型为静态形状）；
     *         调用方必须把它当失败处理，不能假定「已生效」
     */
    bool set_input_size(int w, int h) noexcept {
        if (kDetectionInputShapeIsStatic) {
            if (w != kDefaultModelWidth || h != kDefaultModelHeight) {
                return false;
            }
        } else if (!is_supported_detection_input(w, h, kFaceInputGranularity)) {
            return false;
        }
        model_width_ = w;
        model_height_ = h;
        return true;
    }

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
    int model_width_{kDefaultModelWidth};
    int model_height_{kDefaultModelHeight};
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
    PersonDetector(int model_width, int model_height) noexcept
        : model_width_(model_width), model_height_(model_height) {}

    int model_width() const noexcept { return model_width_; }
    int model_height() const noexcept { return model_height_; }

    /**
     * @brief 设置人体检测输入分辨率
     * @return false 表示该分辨率不可用（未整除 128，或本档位模型为静态形状）
     */
    bool set_input_size(int w, int h) noexcept {
        if (kDetectionInputShapeIsStatic) {
            if (w != kDefaultModelWidth || h != kDefaultModelHeight) {
                return false;
            }
        } else if (!is_supported_detection_input(w, h, kPersonInputGranularity)) {
            return false;
        }
        model_width_ = w;
        model_height_ = h;
        return true;
    }

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
    int model_width_{kDefaultModelWidth};
    int model_height_{kDefaultModelHeight};
    float score_threshold_{kDefaultDetectionScoreThreshold};
    float nms_threshold_{kDefaultNmsIouThreshold};
};

} // namespace zhulong::face
