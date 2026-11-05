/**
 * @file person_detector.cpp
 * @brief 基于 SCRFD 2.5G 的人体检测实现
 */

#include "person_detector.hpp"

#include "common/runner_log.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <exception>
#include <queue>
#include <vector>

namespace zhulong::crowd {

namespace {

constexpr std::string_view kModuleName = "crowd_gathering";

/** @brief SCRFD 归一化参数（换算到 [-1, 1] 区间） */
constexpr float kDetectMean = 127.5f;
constexpr float kDetectInvStd = 1.0f / 128.0f;

/** @brief 256 项归一化不可变查找表（Per.11: 编译期生成，消除每帧 73 万次浮点运算） */
constexpr auto kNormLut = []() constexpr {
    std::array<float, 256> table{};
    for (std::size_t i = 0; i < 256; ++i) {
        table[i] = (static_cast<float>(i) - kDetectMean) * kDetectInvStd;
    }
    return table;
}();

/** @brief 人体检测模型的 FPN 步长表 */
constexpr std::array<int, 5> kPersonFpnStrides{8, 16, 32, 64, 128};
/** @brief 每格 anchor 数（scrfd_person_2.5g 为 1；人脸模型才是 2，混用会整帧零检出） */
constexpr int kPersonAnchorsPerCell = 1;
/** @brief 解码阶段的候选上限（按分数保留 top-N，再送 NMS） */
constexpr std::size_t kMaxPersonCandidates = 256;

constexpr std::size_t kScrfScoreValuesPerAnchor = 1;
constexpr std::size_t kScrfBoxValuesPerAnchor = 4;
constexpr std::size_t kScrfKpsValuesPerAnchor = 10; // 5 landmarks * 2（本模型解码不消费，仅用于校验布局）

struct ScrfCandidate {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 像素坐标 [x1, y1, x2, y2] */
    float score{0.0f};
};

/** @brief 小顶堆比较器：`top()` 是分数最小的候选 */
struct ScrfCandidateMinScoreFirst {
    bool operator()(const ScrfCandidate& a, const ScrfCandidate& b) const noexcept {
        return a.score > b.score;
    }
};

/** @brief 元素数查询；维度非法时 SDK 会抛，这里折成 0（调用方按「布局不符」处理） */
std::size_t safe_element_count(const zhulong::Tensor& tensor) noexcept {
    try {
        return tensor.element_count();
    } catch (const std::exception&) {
        return 0;
    }
}

bool hwc_to_nchw_normalized(const uint8_t* src_hwc, int height, int width, float* dst_nchw) noexcept {
    if (src_hwc == nullptr || dst_nchw == nullptr || height <= 0 || width <= 0) {
        return false;
    }
    const std::size_t plane_size = static_cast<std::size_t>(width) * static_cast<std::size_t>(height);
    // 三个平面共用同一 LUT，按物理平面连续写入，流水线与缓存友好
    float* plane0 = dst_nchw;
    float* plane1 = dst_nchw + plane_size;
    float* plane2 = dst_nchw + plane_size * 2;

    for (std::size_t i = 0; i < plane_size; ++i) {
        plane0[i] = kNormLut[src_hwc[i * 3 + 0]];
        plane1[i] = kNormLut[src_hwc[i * 3 + 1]];
        plane2[i] = kNormLut[src_hwc[i * 3 + 2]];
    }
    return true;
}

/**
 * @brief 校验 SCRFD 输出张量布局
 *
 * 只数张量个数并不够：换量化档位或重导出模型时个数可能不变，而每格 anchor 数、空间
 * 尺度或数据类型已经变了 —— 解码循环仍按旧布局索引就会越界读。这里把 score / bbox / kps
 * 三组的 dtype 与 element_count 逐项对齐，任何一项不符即整体放弃解码。
 *
 * ⚠️ 失败时**必须留下一条记录**：布局不符的默认表现是「永远零检出、一切正常」，
 *    这正是 spec §4 警告的静默零检出。
 */
template<std::size_t N>
bool scrfd_outputs_match(const std::vector<zhulong::Tensor>& outputs,
                         const std::array<int, N>& strides,
                         int anchors_per_cell,
                         int model_width,
                         int model_height,
                         const char** out_reason) noexcept {
    if (outputs.size() != N * 3) {
        *out_reason = "unexpected output tensor count";
        return false;
    }
    if (anchors_per_cell <= 0 || model_width <= 0 || model_height <= 0) {
        *out_reason = "invalid detector geometry";
        return false;
    }

    for (std::size_t idx = 0; idx < N; ++idx) {
        const int stride = strides[idx];
        if (stride <= 0 || model_height % stride != 0 || model_width % stride != 0) {
            *out_reason = "model input size is not divisible by fpn stride";
            return false;
        }
        const std::size_t grid_h = static_cast<std::size_t>(model_height / stride);
        const std::size_t grid_w = static_cast<std::size_t>(model_width / stride);
        const std::size_t anchors = grid_h * grid_w * static_cast<std::size_t>(anchors_per_cell);

        const std::array<std::size_t, 3> expected{
            anchors * kScrfScoreValuesPerAnchor,
            anchors * kScrfBoxValuesPerAnchor,
            anchors * kScrfKpsValuesPerAnchor,
        };
        for (std::size_t group = 0; group < expected.size(); ++group) {
            const auto& tensor = outputs[group * N + idx];
            if (tensor.dtype() != zhulong::DataType::FLOAT32) {
                *out_reason = "output tensor dtype is not float32";
                return false;
            }
            if (safe_element_count(tensor) != expected[group]) {
                *out_reason = "output tensor element count does not match anchors per cell";
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

DetectorWorkspace::DetectorWorkspace(int model_width, int model_height)
    : model_width_(model_width),
      model_height_(model_height),
      letterboxed_(std::vector<int64_t>{model_height, model_width, 3}, zhulong::DataType::UINT8) {
    inputs_.reserve(1);
    inputs_.emplace_back(std::vector<int64_t>{1, 3, model_height, model_width}, zhulong::DataType::FLOAT32);
}

std::vector<DetectedPerson> PersonDetector::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    float score_threshold,
    DetectorWorkspace& workspace
) const {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0 || !session.valid()) {
        return {};
    }
    if (workspace.model_width() != model_width_ || workspace.model_height() != model_height_) {
        // 工作区与模型输入尺寸失配（配置在 init 后被改动过）：宁可报错也不要越界写
        if (workspace.first_geometry_mismatch()) {
            log_warning(kModuleName, "detector workspace size mismatch with model input", "geometry");
        }
        return {};
    }

    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int model_w = model_width_;
    const int model_h = model_height_;

    // 1. 等比缩放与 Letterbox 前处理（写入复用的工作区）
    zhulong::LetterboxParams params;
    params.target_width = model_w;
    params.target_height = model_h;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    if (zhulong::ops::letterbox(image, workspace.letterboxed_tensor(), params) != ZHULONG_PLUGIN_OK) {
        return {};
    }

    // 2. 转换成 NCHW float32（就地写入工作区的输入张量，使用编译期查表）
    if (!hwc_to_nchw_normalized(workspace.letterboxed_tensor().data<uint8_t>(), model_h, model_w,
                                workspace.model_input().data<float>())) {
        return {};
    }

    // 3. 模型前向推理（按 const 引用传入，Tensor 不会被整块拷贝）
    const std::vector<zhulong::Tensor> outputs = session.forward(workspace.model_inputs());
    const char* layout_reason = "no output";
    if (!scrfd_outputs_match(outputs, kPersonFpnStrides, kPersonAnchorsPerCell, model_w, model_h,
                             &layout_reason)) {
        if (workspace.first_layout_mismatch()) {
            log_warning(kModuleName, "scrfd output layout mismatch, detection disabled", layout_reason);
        }
        return {};
    }

    const float det_scale = params.scale;
    const float pad_x = static_cast<float>(params.pad_left);
    const float pad_y = static_cast<float>(params.pad_top);
    if (det_scale <= 0.0f) {
        return {};
    }

    // 4. SCRFD 多尺度输出解码：全网格扫描 + 定长小顶堆按分数保留 top-N
    std::priority_queue<ScrfCandidate, std::vector<ScrfCandidate>, ScrfCandidateMinScoreFirst> top_candidates;
    constexpr std::size_t N = kPersonFpnStrides.size();

    for (std::size_t idx = 0; idx < N; ++idx) {
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
                    if (!(score >= score_threshold)) { // 同时排除 NaN
                        continue;
                    }
                    // 堆满且不高于堆顶最小值时，直接跳过昂贵的几何反算
                    if (top_candidates.size() >= kMaxPersonCandidates && score <= top_candidates.top().score) {
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

    // 5. 归一化 + NMS（复用运行时算子：IoU 在逐轴线性缩放下不变，因此像素空间的候选
    //    归一化后再做 NMS 与在像素空间做的结果一致）
    std::vector<zhulong::Detection> candidates;
    candidates.reserve(top_candidates.size());
    while (!top_candidates.empty()) {
        const ScrfCandidate& candidate = top_candidates.top();
        zhulong::Detection detection;
        const std::array<float, 4> normalized = to_normalized_box(candidate.box, orig_w, orig_h);
        detection.box[0] = normalized[0];
        detection.box[1] = normalized[1];
        detection.box[2] = normalized[2];
        detection.box[3] = normalized[3];
        detection.confidence = candidate.score;
        detection.class_id = 0;
        detection.label = "person";
        candidates.push_back(detection);
        top_candidates.pop();
    }

    const std::vector<zhulong::Detection> kept = zhulong::ops::nms(candidates, nms_threshold_);

    std::vector<DetectedPerson> results;
    results.reserve(kept.size());
    for (const auto& detection : kept) {
        DetectedPerson person;
        person.box = {detection.box[0], detection.box[1], detection.box[2], detection.box[3]};
        person.score = detection.confidence;
        results.push_back(person);
    }
    return results;
}

} // namespace zhulong::crowd
