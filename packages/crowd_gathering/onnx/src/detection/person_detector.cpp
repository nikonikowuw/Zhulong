/**
 * @file person_detector.cpp
 * @brief 基于 SCRFD 2.5G 的人体检测实现
 */

#include "person_detector.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <queue>
#include <vector>

namespace zhulong::crowd {

namespace {

constexpr float kDetectMean = 127.5f;
constexpr float kDetectInvStd = 1.0f / 128.0f;

constexpr std::array<int, 5> kPersonFpnStrides{8, 16, 32, 64, 128};
constexpr int kPersonAnchorsPerCell = 1;
constexpr size_t kMaxPersonCandidates = 256;

constexpr size_t kScrfScoreValuesPerAnchor = 1;
constexpr size_t kScrfBoxValuesPerAnchor = 4;
constexpr size_t kScrfKpsValuesPerAnchor = 10; // 5 landmarks * 2

struct ScrfCandidate {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 像素坐标 [x1, y1, x2, y2] */
    float score{0.0f};
};

struct ScrfCandidateMinScoreFirst {
    bool operator()(const ScrfCandidate& a, const ScrfCandidate& b) const noexcept {
        return a.score > b.score;
    }
};

float calculate_iou(const ScrfCandidate& a, const ScrfCandidate& b) noexcept {
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

bool hwc_to_nchw_normalized(const uint8_t* src_hwc, int height, int width,
                            float mean, float inv_std, float* dst_nchw) noexcept {
    if (src_hwc == nullptr || dst_nchw == nullptr || height <= 0 || width <= 0) {
        return false;
    }
    const size_t plane_size = static_cast<size_t>(width) * static_cast<size_t>(height);
    float* dst_r = dst_nchw;
    float* dst_g = dst_nchw + plane_size;
    float* dst_b = dst_nchw + plane_size * 2;

    for (size_t i = 0; i < plane_size; ++i) {
        dst_r[i] = (static_cast<float>(src_hwc[i * 3 + 0]) - mean) * inv_std;
        dst_g[i] = (static_cast<float>(src_hwc[i * 3 + 1]) - mean) * inv_std;
        dst_b[i] = (static_cast<float>(src_hwc[i * 3 + 2]) - mean) * inv_std;
    }
    return true;
}

size_t safe_element_count(const zhulong::Tensor& tensor) noexcept {
    try {
        return tensor.element_count();
    } catch (...) {
        return 0;
    }
}

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

std::array<float, 4> to_normalized_box(const std::array<float, 4>& pixels, int width, int height) noexcept {
    const float w = static_cast<float>(width);
    const float h = static_cast<float>(height);
    return {
        std::clamp(pixels[0] / w, 0.0f, 1.0f),
        std::clamp(pixels[1] / h, 0.0f, 1.0f),
        std::clamp(pixels[2] / w, 0.0f, 1.0f),
        std::clamp(pixels[3] / h, 0.0f, 1.0f)
    };
}

} // namespace

std::vector<DetectedPerson> PersonDetector::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    float score_threshold
) const {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0 || !session.valid()) {
        return {};
    }

    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    // 1. 等比缩放与 Letterbox 前处理
    zhulong::Tensor letterboxed({1, model_h, model_w, 3}, zhulong::DataType::UINT8);
    zhulong::LetterboxParams params;
    params.target_width = model_w;
    params.target_height = model_h;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    if (zhulong::ops::letterbox(image, letterboxed, params) != ZHULONG_PLUGIN_OK) {
        return {};
    }

    // 2. 转换成 NCHW float32
    zhulong::Tensor onnx_input({1, 3, model_h, model_w}, zhulong::DataType::FLOAT32);
    if (!hwc_to_nchw_normalized(letterboxed.data<uint8_t>(), model_h, model_w,
                                kDetectMean, kDetectInvStd, onnx_input.data<float>())) {
        return {};
    }

    // 3. 模型前向推理
    const std::vector<zhulong::Tensor> outputs = session.forward({onnx_input});
    if (!scrfd_outputs_match(outputs, kPersonFpnStrides, kPersonAnchorsPerCell, model_w, model_h)) {
        return {};
    }

    // 4. SCRFD 多尺度输出解码
    std::priority_queue<ScrfCandidate, std::vector<ScrfCandidate>, ScrfCandidateMinScoreFirst> top_candidates;
    constexpr size_t N = kPersonFpnStrides.size();

    const float det_scale = params.scale;
    const float pad_x = static_cast<float>(params.pad_left);
    const float pad_y = static_cast<float>(params.pad_top);

    if (det_scale <= 0.0f) {
        return {};
    }

    for (size_t idx = 0; idx < N; ++idx) {
        const int stride = kPersonFpnStrides[idx];
        const int grid_h = model_h / stride;
        const int grid_w = model_w / stride;

        const float* scores = outputs[idx].data<float>();
        const float* bboxes = outputs[idx + N].data<float>();
        if (scores == nullptr || bboxes == nullptr) {
            continue;
        }

        for (int h = 0; h < grid_h; ++h) {
            for (int w = 0; w < grid_w; ++w) {
                for (int a = 0; a < kPersonAnchorsPerCell; ++a) {
                    const int index = (h * grid_w + w) * kPersonAnchorsPerCell + a;
                    const float score = scores[index];
                    if (!(score >= score_threshold)) {
                        continue;
                    }

                    const float center_x = static_cast<float>(w * stride);
                    const float center_y = static_cast<float>(h * stride);

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

                    top_candidates.push(candidate);
                    if (top_candidates.size() > kMaxPersonCandidates) {
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

    // 5. NMS 过滤
    std::vector<bool> suppressed(candidates.size(), false);
    std::vector<DetectedPerson> results;
    results.reserve(candidates.size());

    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) {
            continue;
        }
        DetectedPerson person;
        person.box = to_normalized_box(candidates[i].box, orig_w, orig_h);
        person.score = candidates[i].score;
        results.push_back(person);

        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (!suppressed[j] && calculate_iou(candidates[i], candidates[j]) > nms_threshold_) {
                suppressed[j] = true;
            }
        }
    }

    return results;
}

} // namespace zhulong::crowd
