/**
 * @file face_quality.hpp
 * @brief 基于几何与关键点拓扑的纯数据逻辑人脸质量评估器
 *
 * 为什么用纯数据逻辑：
 *   1. 零模型依赖：RK3568 算力仅 1 TOPS，附加神经网络质检模型会造成巨大的 NPU/CPU 开销；
 *   2. 极低延迟：纯几何加减乘除与统计比例计算，耗时 < 1 微秒，零内存分配；
 *   3. 物理可解释：偏航角（Yaw 对称性）、俯仰角（Pitch 结构比）、尺寸、长宽比各分量清晰独立；
 *   4. 算力防护卫士：在昂贵的 512 维特征抽取前置门禁拦截低质目标，杜绝误识和算力浪费。
 */

#pragma once

#include "detection/detector.hpp"

#include <algorithm>
#include <array>
#include <cmath>

namespace zhulong::face {

struct FaceQualityAssessment {
    bool pass{false};           /**< 是否通过尺寸与综合质量门禁 */
    float quality_score{0.0f};  /**< 纯数据逻辑综合质量评分 [0.0, 1.0] */
    float yaw_score{0.0f};      /**< 水平对称性 / 偏航角分量 [0.0, 1.0] (1.0 最佳) */
    float pitch_score{0.0f};    /**< 垂直结构 / 俯仰角分量 [0.0, 1.0] (1.0 最佳) */
    float aspect_score{0.0f};   /**< 宽高比合理性分量 [0.0, 1.0] */
    float size_score{0.0f};     /**< 像素尺寸达标分量 [0.0, 1.0] */
    float pixel_width{0.0f};    /**< 原图人脸像素宽度 */
    float pixel_height{0.0f};   /**< 原图人脸像素高度 */
};

/**
 * @brief 纯数据逻辑人脸质量评估
 *
 * 评估维度：
 *   1. 尺寸硬门槛 (min_face_size)：实际人脸宽或高小于阈值直接否决 (质量分 0)；
 *   2. 宽高比合理性 (Aspect Ratio)：人脸正常高宽比约在 [0.9, 1.5]，过扁(<0.6)或过长(>2.2)严重衰减；
 *   3. 偏航对称性 (Yaw Symmetry)：基于左右眼-鼻尖水平距离比及左右嘴角的水平对称比。正脸趋近 1.0，侧脸衰减；
 *   4. 俯仰结构性 (Pitch Structure)：基于双眼-鼻尖-嘴巴垂直相对位置比例。正常正脸鼻尖在眼嘴 38%~65% 之间；
 *   5. 综合加权评分：综合置信度与几何拓扑质量，输出 [0.0, 1.0] 质量分。
 *
 * @param detection 人脸检测对象 (含 face_box, landmarks, face_score)
 * @param image_width 原始图像宽度
 * @param image_height 原始图像高度
 * @param min_face_size 最小人脸像素尺寸要求 (<=0 表示不限制)
 * @param min_face_quality 综合质量分门限 (<=0 表示不限制)
 * @return 质量评估结果
 */
inline FaceQualityAssessment assess_face_quality(
    const FaceDetection& detection,
    int image_width,
    int image_height,
    int min_face_size,
    float min_face_quality
) noexcept {
    FaceQualityAssessment res;
    if (image_width <= 0 || image_height <= 0) {
        return res;
    }

    const float norm_w = detection.face_box[2] - detection.face_box[0];
    const float norm_h = detection.face_box[3] - detection.face_box[1];
    if (norm_w <= 0.0f || norm_h <= 0.0f) {
        return res;
    }

    res.pixel_width = norm_w * static_cast<float>(image_width);
    res.pixel_height = norm_h * static_cast<float>(image_height);
    const float min_dim = std::min(res.pixel_width, res.pixel_height);

    // 1. 尺寸评估 (硬门槛与软打分)
    if (min_face_size > 0 && min_dim < static_cast<float>(min_face_size)) {
        res.size_score = 0.0f;
        res.quality_score = 0.0f;
        res.pass = false;
        return res;
    }

    const float target_size = (min_face_size > 0)
        ? std::max(64.0f, static_cast<float>(min_face_size) * 1.5f)
        : 64.0f;
    if (min_dim >= target_size) {
        res.size_score = 1.0f;
    } else {
        const float floor_sz = (min_face_size > 0) ? static_cast<float>(min_face_size) : 16.0f;
        const float span = target_size - floor_sz;
        res.size_score = (span > 1e-4f)
            ? std::clamp(0.7f + 0.3f * ((min_dim - floor_sz) / span), 0.0f, 1.0f)
            : 1.0f;
    }

    // 2. 宽高比合理性 (正常人脸 h/w 约在 0.9 ~ 1.5 之间)
    const float ar = res.pixel_height / (res.pixel_width + 1e-5f);
    if (ar >= 0.9f && ar <= 1.5f) {
        res.aspect_score = 1.0f;
    } else if (ar < 0.9f) {
        res.aspect_score = std::clamp((ar - 0.5f) / 0.4f, 0.0f, 1.0f);
    } else {
        res.aspect_score = std::clamp((2.2f - ar) / 0.7f, 0.0f, 1.0f);
    }

    // 3. 姿态与对称性分析 (基于 5 点关键点: 0=左眼, 1=右眼, 2=鼻尖, 3=左嘴, 4=右嘴)
    if (detection.landmarks.size() >= 5) {
        const auto& le = detection.landmarks[0];
        const auto& re = detection.landmarks[1];
        const auto& nose = detection.landmarks[2];
        const auto& lm = detection.landmarks[3];
        const auto& rm = detection.landmarks[4];

        // 3.1 偏航对称比 (Yaw / Horizontal Symmetry)
        const float d_le = std::abs(nose.x - le.x);
        const float d_re = std::abs(re.x - nose.x);
        const float eye_span = std::abs(re.x - le.x);

        float r_eye = 0.0f;
        if (eye_span > 1e-5f) {
            r_eye = std::min(d_le, d_re) / (std::max(d_le, d_re) + 1e-5f);
        }

        const float d_lm = std::abs(nose.x - lm.x);
        const float d_rm = std::abs(rm.x - nose.x);
        const float mouth_span = std::abs(rm.x - lm.x);
        float r_mouth = 0.0f;
        if (mouth_span > 1e-5f) {
            r_mouth = std::min(d_lm, d_rm) / (std::max(d_lm, d_rm) + 1e-5f);
        }

        // 综合水平对称度：眼部权重 0.7，嘴部权重 0.3
        const float r_sym = (mouth_span > 1e-5f) ? (0.7f * r_eye + 0.3f * r_mouth) : r_eye;
        if (r_sym >= 0.55f) {
            res.yaw_score = 1.0f;
        } else {
            res.yaw_score = std::clamp((r_sym - 0.15f) / 0.40f, 0.0f, 1.0f);
        }

        // 3.2 俯仰角结构比 (Pitch / Vertical Position)
        const float eye_y = (le.y + re.y) * 0.5f;
        const float mouth_y = (lm.y + rm.y) * 0.5f;
        const float face_h = mouth_y - eye_y;

        if (face_h <= 0.005f) {
            // 嘴巴比眼睛高，严重倒立或关键点紊乱
            res.pitch_score = 0.0f;
        } else {
            const float r_pitch = (nose.y - eye_y) / (face_h + 1e-5f);
            if (r_pitch >= 0.38f && r_pitch <= 0.65f) {
                res.pitch_score = 1.0f;
            } else if (r_pitch < 0.38f) {
                // 仰头：鼻尖上移接近双眼
                res.pitch_score = std::clamp((r_pitch - 0.15f) / 0.23f, 0.0f, 1.0f);
            } else {
                // 低头：鼻尖下移接近嘴角
                res.pitch_score = std::clamp((0.85f - r_pitch) / 0.20f, 0.0f, 1.0f);
            }
        }
    } else {
        // 无关键点降级
        res.yaw_score = 0.6f;
        res.pitch_score = 0.6f;
    }

    // 4. 综合加权打分 (0.0 ~ 1.0)
    const float geom_score = 0.35f * res.yaw_score + 0.20f * res.pitch_score +
                             0.15f * res.aspect_score + 0.30f * res.size_score;
    const float det_factor = std::clamp(detection.face_score, 0.0f, 1.0f);
    res.quality_score = std::clamp(det_factor * (0.35f + 0.65f * geom_score), 0.0f, 1.0f);

    // 5. 门槛判定
    res.pass = (min_face_quality <= 0.0f || res.quality_score >= min_face_quality);
    return res;
}

} // namespace zhulong::face
