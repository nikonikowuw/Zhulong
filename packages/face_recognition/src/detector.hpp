/**
 * @file detector.hpp
 * @brief 人脸检测、5点关键点定位与人体空间关联模块
 */

#pragma once

#include "aligner.hpp"
#include <zhulong/algo.hpp>
#include <platform/dmabuf/dmabuf_allocator.hpp>
#include <array>
#include <memory>
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
    std::array<float, 4> face_box_pixels{0.0f, 0.0f, 0.0f, 0.0f}; /**< 原帧像素坐标 [x1, y1, x2, y2] */
    std::array<Point2f, 5> landmarks_pixels{};              /**< 原帧像素坐标 5 点关键点 */
};

class FaceDetector {
public:
    FaceDetector() = default;

    /**
     * @brief 基于推理会话执行真实 NPU 人脸与人体检测 (SCRFD on RKNN)
     * @param session 当前 Worker 的检测执行会话
     * @param image 输入只读帧视图
     * @return 检出的结构化目标列表 (全黑或无目标图像返回空 vector)
     */
    std::vector<FaceDetection> detect(zhulong::Session& session, const zhulong::Image& image);

    /**
     * @brief 对输入图像执行人脸与人体检测 (CPU 兼容/单测降级路径)
     * @param image 输入只读帧视图
     * @return 检出的结构化目标列表 (全黑或无目标图像返回空 vector)
     */
    std::vector<FaceDetection> detect(const zhulong::Image& image);

    void set_score_threshold(float th) noexcept { score_threshold_ = th; }
    void set_nms_threshold(float th) noexcept { nms_threshold_ = th; }

private:
    std::vector<FaceDetection> detect_cpu_fallback(const zhulong::Image& image);
    std::vector<FaceDetection> decode_scrfd(
        const std::vector<zhulong::Tensor>& outputs,
        int orig_w, int orig_h, float det_scale,
        int pad_left = 0, int pad_top = 0
    );

    float score_threshold_{0.50f};
    float nms_threshold_{0.40f};

    std::unique_ptr<zhulong::platform::DmaBuffer> dma_input_buf_;
    int cached_w_{0};
    int cached_h_{0};
};

} // namespace zhulong::face
