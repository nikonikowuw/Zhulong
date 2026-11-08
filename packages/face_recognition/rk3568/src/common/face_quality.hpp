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
 *   1. 有限性与拓扑硬门禁：非有限输入、零/负面积框、眼距塌缩（< 0.12 * min_dim）、
 *      眼嘴高度塌缩或倒置（< 0.15 * min_dim）、嘴角手性交叉、鼻尖拓扑越界一律硬拒绝；
 *   2. 尺寸硬门槛 (min_face_size)：实际人脸宽或高小于阈值直接否决 (质量分 0)；
 *   3. 宽高比合理性 (Aspect Ratio)：人脸正常高宽比约在 [0.9, 1.5]，过扁(<0.6)或过长(>2.2)严重衰减；
 *   4. 偏航对称性 (Yaw Symmetry)：在像素空间眼线有向轴 u 上统计鼻尖到两眼/两嘴角的有向对称比，
 *      正脸趋近 1.0，侧脸鼻尖越过单眼/单嘴角时归零走软评分衰减（不因绝对值反向反弹）；
 *   5. 俯仰结构性 (Pitch Structure)：在眼线正交轴 v 上统计双眼中心-鼻尖-嘴角中心的相对位置比例；
 *   6. 综合加权评分：综合置信度与几何拓扑质量，输出 [0.0, 1.0] 质量分。
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
    if (image_width <= 0 || image_height <= 0 || !std::isfinite(min_face_quality)) {
        return res;
    }

    if (!std::isfinite(detection.face_box[0]) || !std::isfinite(detection.face_box[1]) ||
        !std::isfinite(detection.face_box[2]) || !std::isfinite(detection.face_box[3]) ||
        !std::isfinite(detection.face_score) || detection.face_score <= 0.0f) {
        return res;
    }

    const float norm_w = detection.face_box[2] - detection.face_box[0];
    const float norm_h = detection.face_box[3] - detection.face_box[1];
    if (!std::isfinite(norm_w) || !std::isfinite(norm_h) || norm_w <= 0.0f || norm_h <= 0.0f) {
        return res;
    }

    const float pixel_w = norm_w * static_cast<float>(image_width);
    const float pixel_h = norm_h * static_cast<float>(image_height);
    if (!std::isfinite(pixel_w) || !std::isfinite(pixel_h) || pixel_w <= 0.0f || pixel_h <= 0.0f) {
        return res;
    }

    const auto pts = pixel_landmarks(detection, image_width, image_height);
    for (const auto& pt : pts) {
        if (!std::isfinite(pt.x) || !std::isfinite(pt.y)) {
            return res;
        }
    }

    res.pixel_width = pixel_w;
    res.pixel_height = pixel_h;
    const float min_dim = std::min(res.pixel_width, res.pixel_height);

    // 1. 尺寸评估 (硬门槛与软打分；容忍 float32 归一化往返 x/W*W 的亚毫像素舍入误差)
    constexpr float kPixelRoundtripEps = 1e-3f;
    if (min_face_size > 0 && min_dim + kPixelRoundtripEps < static_cast<float>(min_face_size)) {
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

    // 2. 宽高比合理性 (正常人脸 h/w 约在 0.9 ~ 1.5 之间；检测框本身不随滚转不变)
    const float ar = res.pixel_height / (res.pixel_width + 1e-5f);
    if (ar >= 0.9f && ar <= 1.5f) {
        res.aspect_score = 1.0f;
    } else if (ar < 0.9f) {
        res.aspect_score = std::clamp((ar - 0.5f) / 0.4f, 0.0f, 1.0f);
    } else {
        res.aspect_score = std::clamp((2.2f - ar) / 0.7f, 0.0f, 1.0f);
    }

    // 3. 像素空间眼线局部有向坐标系与拓扑/姿态评估
    //    (0=左眼, 1=右眼, 2=鼻尖, 3=左嘴角, 4=右嘴角)
    //
    //    必须在等距像素空间旋转：归一化坐标 x/W 与 y/H 尺度不同，直接旋转会把几何按画面宽高比拉斜。
    //    局部横轴 u 指向左眼→右眼，局部纵轴 v = (-u.y, u.x) 在图像坐标系下顺时针正交指向嘴部方向。
    {
        constexpr float kMinEyeSpanRatio = 0.12f;
        constexpr float kMinFaceHeightRatio = 0.15f;
        constexpr float kMaxPitchRatio = 1.10f;
        constexpr float kMaxLateralOutlierRatio = 1.00f;

        const Point2f& le = pts[0];
        const Point2f& re = pts[1];
        const Point2f& nose = pts[2];
        const Point2f& lm = pts[3];
        const Point2f& rm = pts[4];

        const float ex = re.x - le.x;
        const float ey = re.y - le.y;
        const float eye_span = std::hypot(ex, ey);
        if (!std::isfinite(eye_span) || eye_span < kMinEyeSpanRatio * min_dim) {
            res.quality_score = 0.0f;
            res.pass = false;
            return res;
        }

        const Point2f u{ex / eye_span, ey / eye_span};
        const Point2f v{-u.y, u.x};

        const Point2f eye_mid{(le.x + re.x) * 0.5f, (le.y + re.y) * 0.5f};
        const Point2f mouth_mid{(lm.x + rm.x) * 0.5f, (lm.y + rm.y) * 0.5f};

        // 左右嘴角沿眼轴必须保持正手性，眼嘴正交距离必须达到最小拓扑高度（拦截倒立与五官塌缩）
        const float mouth_span = (rm.x - lm.x) * u.x + (rm.y - lm.y) * u.y;
        const float face_h = (mouth_mid.x - eye_mid.x) * v.x + (mouth_mid.y - eye_mid.y) * v.y;
        if (!std::isfinite(mouth_span) || mouth_span <= 0.0f ||
            !std::isfinite(face_h) || face_h < kMinFaceHeightRatio * min_dim) {
            res.pitch_score = 0.0f;
            res.quality_score = 0.0f;
            res.pass = false;
            return res;
        }

        // 横向离群防卫：允许普通大侧脸（鼻尖略越过单眼，|nose_u| ≈ 0.2~0.4 * min_dim），
        // 但拦截关键点飞出检测框远端的畸形预测，防止 min_face_quality=0 时漏过坏框。
        const float nose_u = (nose.x - eye_mid.x) * u.x + (nose.y - eye_mid.y) * u.y;
        const float mouth_u = (mouth_mid.x - eye_mid.x) * u.x + (mouth_mid.y - eye_mid.y) * u.y;
        if (!std::isfinite(nose_u) || !std::isfinite(mouth_u) ||
            std::abs(nose_u) > kMaxLateralOutlierRatio * min_dim ||
            std::abs(mouth_u) > kMaxLateralOutlierRatio * min_dim) {
            res.quality_score = 0.0f;
            res.pass = false;
            return res;
        }

        // 3.1 俯仰角结构比与垂直拓扑校验
        const float nose_v = (nose.x - eye_mid.x) * v.x + (nose.y - eye_mid.y) * v.y;
        const float r_pitch = nose_v / face_h;
        if (!std::isfinite(r_pitch) || r_pitch <= 0.0f || r_pitch >= kMaxPitchRatio) {
            res.pitch_score = 0.0f;
            res.quality_score = 0.0f;
            res.pass = false;
            return res;
        }

        if (r_pitch >= 0.38f && r_pitch <= 0.65f) {
            res.pitch_score = 1.0f;
        } else if (r_pitch < 0.38f) {
            // 仰头：鼻尖上移接近双眼
            res.pitch_score = std::clamp((r_pitch - 0.15f) / 0.23f, 0.0f, 1.0f);
        } else {
            // 低头：鼻尖下移接近嘴角（保留俯视 CCTV 大俯仰余量至 kMaxPitchRatio）
            res.pitch_score = std::clamp((0.85f - r_pitch) / 0.20f, 0.0f, 1.0f);
        }

        // 3.2 有向偏航对称比 (Yaw / Horizontal Symmetry)
        // 只有鼻尖投影严格落在左/右端点之间（两端有向距离均 > 0）才计入对称度；
        // 一旦越过单侧端点（如 >48° 大侧脸三维鼻梁投影越过外眼角），直接置 0 走软评分衰减，
        // 避免旧实现 std::abs 让远端越界点比值反向回升趋近 1.0。
        const float d_le = (nose.x - le.x) * u.x + (nose.y - le.y) * u.y;
        const float d_re = (re.x - nose.x) * u.x + (re.y - nose.y) * u.y;
        float r_eye = 0.0f;
        if (d_le > 0.0f && d_re > 0.0f) {
            const float max_eye = std::max(d_le, d_re);
            if (max_eye > 0.0f) {
                r_eye = std::min(d_le, d_re) / max_eye;
            }
        }

        const float d_lm = (nose.x - lm.x) * u.x + (nose.y - lm.y) * u.y;
        const float d_rm = (rm.x - nose.x) * u.x + (rm.y - nose.y) * u.y;
        float r_mouth = 0.0f;
        if (d_lm > 0.0f && d_rm > 0.0f) {
            const float max_mouth = std::max(d_lm, d_rm);
            if (max_mouth > 0.0f) {
                r_mouth = std::min(d_lm, d_rm) / max_mouth;
            }
        }

        // 综合水平对称度：眼部权重 0.7，嘴部权重 0.3
        const float r_sym = 0.7f * r_eye + 0.3f * r_mouth;
        if (r_sym >= 0.55f) {
            res.yaw_score = 1.0f;
        } else {
            res.yaw_score = std::clamp((r_sym - 0.15f) / 0.40f, 0.0f, 1.0f);
        }
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
