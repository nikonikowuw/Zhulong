#include "detector.hpp"

#include "common/face_preprocess.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <exception>
#include <queue>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief 检测前处理归一化参数：(x - kDetectMean) * kDetectInvStd，RGB 顺序 */
constexpr float kDetectMean = 127.5f;
constexpr float kDetectInvStd = 1.0f / 128.0f;

/** @brief FPN 各层 stride；人脸 3 层、人体 5 层 */
constexpr std::array<int, 3> kFaceFpnStrides{8, 16, 32};
constexpr std::array<int, 5> kPersonFpnStrides{8, 16, 32, 64, 128};
/** @brief 每格 anchor 数：det_500m 为 2，Person 2.5G 为 1 */
constexpr int kFaceAnchorsPerCell = 2;
constexpr int kPersonAnchorsPerCell = 1;

/** @brief 单帧候选框上限：防病态输入把 NMS 拖成 O(n²) */
constexpr size_t kMaxFaceCandidates = 128;
constexpr size_t kMaxPersonCandidates = 128;

/** @brief SCRFD 每个 anchor 的输出宽度：score(1) / bbox(4) / kps(5 点 × 2) */
constexpr size_t kScrfScoreValuesPerAnchor = 1;
constexpr size_t kScrfBoxValuesPerAnchor = 4;
constexpr size_t kScrfKpsValuesPerAnchor = kLandmarkCount * 2;

/**
 * @brief 解码阶段的统一候选框（人脸与人体共用）
 *
 * 两个检测器的 stride 表/anchor 数/输出类型不同，但「格子 → 像素框 + 关键点」的
 * 数学完全一致，因此解码只写一份。
 */
struct ScrfCandidate {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 原帧像素坐标 [x1, y1, x2, y2] */
    float score{0.0f};
    std::array<Point2f, kLandmarkCount> kps{};        /**< 原帧像素坐标关键点 */
};

/**
 * @brief 候选收集用小顶堆比较器：堆顶恒为当前**最低分**，便于「满员后只顶替更低分者」
 */
struct ScrfCandidateMinScoreFirst {
    bool operator()(const ScrfCandidate& a, const ScrfCandidate& b) const noexcept {
        return a.score > b.score;
    }
};

float calculate_iou(const ScrfCandidate& a, const ScrfCandidate& b) {
    const float x1 = std::max(a.box[0], b.box[0]);
    const float y1 = std::max(a.box[1], b.box[1]);
    const float x2 = std::min(a.box[2], b.box[2]);
    const float y2 = std::min(a.box[3], b.box[3]);
    const float inter_w = std::max(0.0f, x2 - x1);
    const float inter_h = std::max(0.0f, y2 - y1);
    const float inter = inter_w * inter_h;
    const float area_a = std::max(0.0f, a.box[2] - a.box[0]) * std::max(0.0f, a.box[3] - a.box[1]);
    const float area_b = std::max(0.0f, b.box[2] - b.box[0]) * std::max(0.0f, b.box[3] - b.box[1]);
    const float u = area_a + area_b - inter;
    return (u <= 0.0f) ? 0.0f : (inter / u);
}

/** @brief 元素数查询；维度非法时 SDK 会抛，这里折成 0（调用方按「布局不符」处理） */
size_t safe_element_count(const zhulong::Tensor& tensor) noexcept {
    try {
        return tensor.element_count();
    } catch (const std::exception&) {
        return 0;
    }
}

/**
 * @brief 校验 SCRFD 输出张量布局
 *
 * 只数张量个数（9 / 15）并不够：换量化档位或重导出模型时个数可能不变，而每格
 * anchor 数、空间尺度或数据类型已经变了 —— 解码循环仍按旧布局索引，就越界读到
 * 别的对象。这里把 score / bbox / kps 三组的 dtype 与 element_count 逐项对齐，
 * 任何一项不符就整体放弃解码（契约：输出形状不合法时返回空结果）。
 */
template<size_t N>
bool scrfd_outputs_match(const std::vector<zhulong::Tensor>& outputs,
                         const std::array<int, N>& strides,
                         int anchors_per_cell,
                         int model_width,
                         int model_height) {
    if (outputs.size() != N * 3 || anchors_per_cell <= 0 || model_width <= 0 || model_height <= 0) {
        return false;
    }

    for (size_t idx = 0; idx < N; ++idx) {
        const int stride = strides[idx];
        if (stride <= 0 || model_height % stride != 0 || model_width % stride != 0) {
            return false;
        }
        const size_t grid_h = static_cast<size_t>(model_height / stride);
        const size_t grid_w = static_cast<size_t>(model_width / stride);
        const size_t anchors = grid_h * grid_w * static_cast<size_t>(anchors_per_cell);

        const std::array<size_t, 3> expected{
            anchors * kScrfScoreValuesPerAnchor,
            anchors * kScrfBoxValuesPerAnchor,
            anchors * kScrfKpsValuesPerAnchor,
        };
        for (size_t group = 0; group < expected.size(); ++group) {
            const auto& tensor = outputs[group * N + idx];
            if (tensor.dtype() != zhulong::DataType::FLOAT32) {
                return false;
            }
            if (safe_element_count(tensor) != expected[group]) {
                return false;
            }
        }
    }
    return true;
}

/**
 * @brief letterbox + HWC(uint8 RGB) → NCHW(float32) 的一体化检测前处理
 *
 * 空间几何参数（缩放比与补边）由 SDK 视觉后端回填，解码阶段据此把框映射回原帧。
 */
bool preprocess_detector_input(const zhulong::Image& image, int model_width, int model_height,
                               zhulong::Tensor& out_nchw, zhulong::LetterboxParams& out_params) {
    if (model_width <= 0 || model_height <= 0) {
        return false;
    }
    zhulong::Tensor letterboxed({1, model_height, model_width, 3}, zhulong::DataType::UINT8);
    out_params.target_width = model_width;
    out_params.target_height = model_height;
    out_params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    if (zhulong::ops::letterbox(image, letterboxed, out_params) != ZHULONG_PLUGIN_OK) {
        return false;
    }
    return hwc_to_nchw_normalized(letterboxed.data<uint8_t>(), model_height, model_width,
                                  kDetectMean, kDetectInvStd, out_nchw.data<float>());
}

/**
 * @brief 按 stride 表解码 SCRFD 三组输出为候选框，并做 NMS
 *
 * 输出张量布局：前 N 个为 score、接着 N 个为 bbox、最后 N 个为 kps（N = stride 层数）。
 */
template<size_t N>
std::vector<ScrfCandidate> decode_scrfd_candidates(const std::vector<zhulong::Tensor>& outputs,
                                                   const std::array<int, N>& strides,
                                                   int anchors_per_cell,
                                                   int model_width,
                                                   int model_height,
                                                   float score_threshold,
                                                   float nms_threshold,
                                                   size_t max_candidates,
                                                   int orig_w, int orig_h,
                                                   float det_scale, int pad_left, int pad_top) {
    if (det_scale <= 0.0f || orig_w <= 0 || orig_h <= 0 || model_width <= 0 || model_height <= 0 ||
        max_candidates == 0 ||
        !scrfd_outputs_match(outputs, strides, anchors_per_cell, model_width, model_height)) {
        return {};
    }

    // 定长小顶堆按**分数**保留 top-max_candidates，而不是「边扫边截断」。
    // 扫描顺序是 stride 由细到粗：一旦满员就 break，先扫到的细层会把粗层（大目标）的
    // 高分框挤掉，而 NMS 是在截断之后才做的 —— 拥挤场景会表现为「远处小脸都在、近处
    // 大脸丢了」。改为全网格扫描 + 定长堆，代价是多扫几千次浮点比较。
    std::priority_queue<ScrfCandidate, std::vector<ScrfCandidate>, ScrfCandidateMinScoreFirst> top_candidates;

    for (size_t idx = 0; idx < N; ++idx) {
        const int stride = strides[idx];
        const int grid_h = model_height / stride;
        const int grid_w = model_width / stride;

        const float* scores = outputs[idx].data<float>();
        const float* bboxes = outputs[idx + N].data<float>();
        const float* kpss = outputs[idx + 2 * N].data<float>();
        if (scores == nullptr || bboxes == nullptr || kpss == nullptr) {
            continue;
        }

        for (int h = 0; h < grid_h; ++h) {
            for (int w = 0; w < grid_w; ++w) {
                for (int a = 0; a < anchors_per_cell; ++a) {
                    const int index = (h * grid_w + w) * anchors_per_cell + a;
                    const float score = scores[index]; // score 已含 Sigmoid，无需重复计算
                    if (!(score >= score_threshold)) { // 同时排除 NaN
                        continue;
                    }

                    const float center_x = static_cast<float>(w * stride);
                    const float center_y = static_cast<float>(h * stride);
                    const float pad_x = static_cast<float>(pad_left);
                    const float pad_y = static_cast<float>(pad_top);

                    // 还原并扣除 Letterbox 居中补边与缩放比，映射回原帧像素坐标
                    ScrfCandidate candidate;
                    candidate.box[0] = std::clamp((center_x - bboxes[index * 4 + 0] * stride - pad_x) / det_scale,
                                                  0.0f, static_cast<float>(orig_w));
                    candidate.box[1] = std::clamp((center_y - bboxes[index * 4 + 1] * stride - pad_y) / det_scale,
                                                  0.0f, static_cast<float>(orig_h));
                    candidate.box[2] = std::clamp((center_x + bboxes[index * 4 + 2] * stride - pad_x) / det_scale,
                                                  0.0f, static_cast<float>(orig_w));
                    candidate.box[3] = std::clamp((center_y + bboxes[index * 4 + 3] * stride - pad_y) / det_scale,
                                                  0.0f, static_cast<float>(orig_h));

                    if (!std::isfinite(candidate.box[0]) || !std::isfinite(candidate.box[1]) ||
                        !std::isfinite(candidate.box[2]) || !std::isfinite(candidate.box[3]) ||
                        candidate.box[2] <= candidate.box[0] || candidate.box[3] <= candidate.box[1]) {
                        continue;
                    }
                    candidate.score = score;

                    bool kps_valid = true;
                    for (size_t k = 0; k < kLandmarkCount; ++k) {
                        const float kx = (center_x + kpss[index * 10 + static_cast<int>(k) * 2 + 0] * stride - pad_x) / det_scale;
                        const float ky = (center_y + kpss[index * 10 + static_cast<int>(k) * 2 + 1] * stride - pad_y) / det_scale;
                        if (!std::isfinite(kx) || !std::isfinite(ky)) {
                            kps_valid = false;
                            break;
                        }
                        candidate.kps[k].x = std::clamp(kx, 0.0f, static_cast<float>(orig_w));
                        candidate.kps[k].y = std::clamp(ky, 0.0f, static_cast<float>(orig_h));
                    }
                    if (!kps_valid) {
                        continue;
                    }

                    top_candidates.push(candidate);
                    if (top_candidates.size() > max_candidates) {
                        top_candidates.pop();
                    }
                }
            }
        }
    }

    if (top_candidates.empty()) {
        return {};
    }

    std::vector<ScrfCandidate> candidates;
    candidates.reserve(top_candidates.size());
    while (!top_candidates.empty()) {
        candidates.push_back(top_candidates.top());
        top_candidates.pop();
    }

    std::sort(candidates.begin(), candidates.end(),
              [](const ScrfCandidate& a, const ScrfCandidate& b) { return a.score > b.score; });

    std::vector<bool> suppressed(candidates.size(), false);
    std::vector<ScrfCandidate> kept;
    kept.reserve(candidates.size());
    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) {
            continue;
        }
        kept.push_back(candidates[i]);
        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (!suppressed[j] && calculate_iou(candidates[i], candidates[j]) > nms_threshold) {
                suppressed[j] = true;
            }
        }
    }
    return kept;
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

std::vector<FaceDetection> FaceDetector::detect(zhulong::Session& session, const zhulong::Image& image) const {
    if (!session.valid() || !image_view_usable(image)) {
        return {};
    }

    const auto& raw = image.raw_view();
    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    zhulong::Tensor onnx_input({1, 3, model_h, model_w}, zhulong::DataType::FLOAT32);
    zhulong::LetterboxParams params;
    if (!preprocess_detector_input(image, model_w, model_h, onnx_input, params)) {
        return {};
    }

    const std::vector<zhulong::Tensor> outputs = session.forward({onnx_input});
    const auto candidates = decode_scrfd_candidates(outputs, kFaceFpnStrides, kFaceAnchorsPerCell,
                                                    model_w, model_h,
                                                    score_threshold_, nms_threshold_, kMaxFaceCandidates,
                                                    orig_w, orig_h, params.scale, params.pad_left, params.pad_top);

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

std::vector<PersonBodyDetection> PersonDetector::detect(zhulong::Session& session,
                                                        const zhulong::Image& image) const {
    if (!session.valid() || !image_view_usable(image)) {
        return {};
    }

    const auto& raw = image.raw_view();
    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    zhulong::Tensor onnx_input({1, 3, model_h, model_w}, zhulong::DataType::FLOAT32);
    zhulong::LetterboxParams params;
    if (!preprocess_detector_input(image, model_w, model_h, onnx_input, params)) {
        return {};
    }

    const std::vector<zhulong::Tensor> outputs = session.forward({onnx_input});
    const auto candidates = decode_scrfd_candidates(outputs, kPersonFpnStrides, kPersonAnchorsPerCell,
                                                    model_w, model_h,
                                                    score_threshold_, nms_threshold_, kMaxPersonCandidates,
                                                    orig_w, orig_h, params.scale, params.pad_left, params.pad_top);

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
