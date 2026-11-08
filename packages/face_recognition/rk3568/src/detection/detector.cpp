#include "detector.hpp"

#include "detection/scrfd_decode.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief FPN 各层 stride；人脸 3 层、人体 5 层 */
constexpr std::array<int, 3> kFaceFpnStrides{8, 16, 32};
constexpr std::array<int, 5> kPersonFpnStrides{8, 16, 32, 64, 128};
/** @brief 每格 anchor 数：det_500m 为 2，Person 2.5G 为 1 */
constexpr int kFaceAnchorsPerCell = 2;
constexpr int kPersonAnchorsPerCell = 1;

/** @brief NMS 前全网格候选保留预算（RK3568 档位：256，兼顾拥挤场景召回与 Cortex-A55 比较成本） */
constexpr size_t kMaxPreNmsFaceCandidates = 256;
constexpr size_t kMaxPreNmsPersonCandidates = 256;
/** @brief NMS 后单帧目标输出上限（同时约束下游每帧 512 维特征提取次数硬上界） */
constexpr size_t kMaxFaceDetections = 128;
constexpr size_t kMaxPersonDetections = 128;

/**
 * @brief letterbox 前处理（RKNN 模型自带均值/方差归一化，直接吃 HWC uint8 RGB）
 *
 * 空间几何参数（缩放比与补边）由 SDK 视觉后端回填，解码阶段据此把框映射回原帧。
 */
bool preprocess_detector_input(const zhulong::Image& image, int model_width, int model_height,
                               zhulong::Tensor& out_hwc, zhulong::LetterboxParams& out_params) {
    if (model_width <= 0 || model_height <= 0) {
        return false;
    }
    out_params.target_width = model_width;
    out_params.target_height = model_height;
    out_params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;
    return zhulong::ops::letterbox(image, out_hwc, out_params) == ZHULONG_PLUGIN_OK;
}

/**
 * @brief 把前处理好的输入张量搬进 forward 的入参列表
 *
 * `Session::forward` 收 `const std::vector<Tensor>&`，直接写 `forward({input})` 会先
 * **深拷贝**一份输入（640×384 的 HWC uint8 = 737 KB，每帧每检测器一份）。先 move 进
 * 局部 vector 再传，省掉这次与推理无关的 memcpy。
 */
std::vector<zhulong::Tensor> single_input(zhulong::Tensor&& tensor) {
    std::vector<zhulong::Tensor> inputs;
    inputs.reserve(1);
    inputs.emplace_back(std::move(tensor));
    return inputs;
}

/** @brief 像素框 → 严格归一化框 [0, 1] */
std::array<float, 4> to_normalized_box(const std::array<float, 4>& pixels, int width, int height) {
    const float w = static_cast<float>(width);
    const float h = static_cast<float>(height);
    return {
        std::clamp(pixels[0] / w, 0.0f, 1.0f),
        std::clamp(pixels[1] / h, 0.0f, 1.0f),
        std::clamp(pixels[2] / w, 0.0f, 1.0f),
        std::clamp(pixels[3] / h, 0.0f, 1.0f)
    };
}

/** @brief 像素关键点 → 严格归一化关键点 [0, 1] */
std::array<Point2f, kLandmarkCount> to_normalized_landmarks(
    const std::array<Point2f, kLandmarkCount>& pixels, int width, int height
) {
    const float w = static_cast<float>(width);
    const float h = static_cast<float>(height);
    std::array<Point2f, kLandmarkCount> normalized{};
    for (size_t i = 0; i < normalized.size(); ++i) {
        normalized[i].x = std::clamp(pixels[i].x / w, 0.0f, 1.0f);
        normalized[i].y = std::clamp(pixels[i].y / h, 0.0f, 1.0f);
    }
    return normalized;
}

/** @brief 帧视图的公共合法性检查（会话与像素之外的部分） */
bool image_view_usable(const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    return raw.planes[0] != nullptr && raw.width > 0 && raw.height > 0 && raw.strides[0] >= 0;
}

} // namespace

std::vector<FaceDetection> FaceDetector::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    float custom_score_threshold) const {
    if (!session.valid() || !image_view_usable(image)) {
        return {};
    }

    const auto& raw = image.raw_view();
    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    zhulong::Tensor input_tensor({1, model_h, model_w, 3}, zhulong::DataType::UINT8);
    zhulong::LetterboxParams params;
    if (!preprocess_detector_input(image, model_w, model_h, input_tensor, params)) {
        return {};
    }

    const float effective_threshold = (custom_score_threshold >= 0.0f) ? custom_score_threshold : score_threshold_;
    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(input_tensor)));
    const auto candidates = decode_scrfd_candidates(outputs, kFaceFpnStrides, kFaceAnchorsPerCell,
                                                    model_w, model_h,
                                                    effective_threshold, nms_threshold_,
                                                    ScrfdDecodeLimits{kMaxPreNmsFaceCandidates,
                                                                      kMaxFaceDetections},
                                                    ScrfdLetterbox{params.scale, params.pad_left,
                                                                   params.pad_top, orig_w, orig_h});

    std::vector<FaceDetection> results;
    results.reserve(candidates.size());
    for (const auto& candidate : candidates) {
        FaceDetection detection;
        detection.face_box = to_normalized_box(candidate.box, orig_w, orig_h);
        detection.face_score = candidate.score;
        detection.landmarks_pixels = candidate.kps;
        detection.has_landmark_pixels = true;
        detection.landmarks = to_normalized_landmarks(candidate.kps, orig_w, orig_h);
        detection.is_real_detection = true;
        results.push_back(detection);
    }
    return results;
}

std::vector<PersonBodyDetection> PersonDetector::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    float custom_score_threshold) const {
    if (!session.valid() || !image_view_usable(image)) {
        return {};
    }

    const auto& raw = image.raw_view();
    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    zhulong::Tensor input_tensor({1, model_h, model_w, 3}, zhulong::DataType::UINT8);
    zhulong::LetterboxParams params;
    if (!preprocess_detector_input(image, model_w, model_h, input_tensor, params)) {
        return {};
    }

    const float effective_threshold = (custom_score_threshold >= 0.0f) ? custom_score_threshold : score_threshold_;
    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(input_tensor)));
    const auto candidates = decode_scrfd_candidates(outputs, kPersonFpnStrides, kPersonAnchorsPerCell,
                                                    model_w, model_h,
                                                    effective_threshold, nms_threshold_,
                                                    ScrfdDecodeLimits{kMaxPreNmsPersonCandidates,
                                                                      kMaxPersonDetections},
                                                    ScrfdLetterbox{params.scale, params.pad_left,
                                                                   params.pad_top, orig_w, orig_h});

    std::vector<PersonBodyDetection> results;
    results.reserve(candidates.size());
    for (const auto& candidate : candidates) {
        PersonBodyDetection detection;
        detection.body_box = to_normalized_box(candidate.box, orig_w, orig_h);
        detection.score = candidate.score;
        detection.is_real_detection = true;
        results.push_back(detection);
    }
    return results;
}

} // namespace zhulong::face
