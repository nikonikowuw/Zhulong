/**
 * @file scrfd_decode.hpp
 * @brief SCRFD 系列输出解码（网格 → 原帧像素框 + 关键点）与 NMS
 *
 * 为什么单独成头文件：这里是整条流水线里**最容易写错、错了又最难发现**的一段。
 * 网格索引、anchor 排布、letterbox 逆变换任何一处偏一位/差一个符号，症状都只是
 * 「恒为零检出」——没有崩溃、没有报错码，端到端看起来就是「图里没人脸」。
 * 因此它必须是**纯数据函数**：不加载模型、不需要会话，包内 `make unit-test`
 * 就能用合成张量把每条数学钉住（见 `.trellis/spec/native/algo-plugin-contract.md` §5.5）。
 *
 * 只依赖 `zhulong::Tensor`（SDK）与 `common/face_types.hpp`，不依赖 `detector.hpp`。
 */

#pragma once

#include "common/face_types.hpp"

#include <zhulong/algo.hpp>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <exception>
#include <queue>
#include <vector>

namespace zhulong::face {

/** @brief SCRFD 每个 anchor 的输出宽度：score(1) / bbox(4) / kps(5 点 × 2) */
inline constexpr size_t kScrfdScoreValuesPerAnchor = 1;
inline constexpr size_t kScrfdBoxValuesPerAnchor = 4;
inline constexpr size_t kScrfdKpsValuesPerAnchor = kLandmarkCount * 2;

/**
 * @brief 解码阶段的统一候选框（人脸与人体共用）
 *
 * 两个检测器的 stride 表/anchor 数/输出类型不同，但「格子 → 像素框 + 关键点」的
 * 数学完全一致，因此解码只写一份。
 */
struct ScrfdCandidate {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 原帧像素坐标 [x1, y1, x2, y2] */
    float score{0.0f};
    std::array<Point2f, kLandmarkCount> kps{};        /**< 原帧像素坐标关键点 */
};

/**
 * @brief 候选收集用小顶堆比较器：堆顶恒为当前**最低分**，便于「满员后只顶替更低分者」
 */
struct ScrfdCandidateMinScoreFirst {
    bool operator()(const ScrfdCandidate& a, const ScrfdCandidate& b) const noexcept {
        return a.score > b.score;
    }
};

/** @brief 两个像素框的 IoU（零/退化框返回 0，绝不返回 NaN） */
inline float scrfd_iou(const ScrfdCandidate& a, const ScrfdCandidate& b) noexcept {
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
inline size_t tensor_element_count_or_zero(const zhulong::Tensor& tensor) noexcept {
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
inline bool scrfd_outputs_match(const std::vector<zhulong::Tensor>& outputs,
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
            anchors * kScrfdScoreValuesPerAnchor,
            anchors * kScrfdBoxValuesPerAnchor,
            anchors * kScrfdKpsValuesPerAnchor,
        };
        for (size_t group = 0; group < expected.size(); ++group) {
            const auto& tensor = outputs[group * N + idx];
            if (tensor.dtype() != zhulong::DataType::FLOAT32) {
                return false;
            }
            if (tensor_element_count_or_zero(tensor) != expected[group]) {
                return false;
            }
        }
    }
    return true;
}

/**
 * @brief 按 stride 表解码 SCRFD 三组输出为候选框，并做 NMS
 *
 * 输出张量布局：前 N 个为 score、接着 N 个为 bbox、最后 N 个为 kps（N = stride 层数）。
 *
 * @param det_scale / pad_left / pad_top letterbox 回填的几何参数（解码据此映射回原帧）
 * @return 抑制后按分数降序的候选；布局不符、参数非法或全部低于阈值时返回空
 */
template<size_t N>
inline std::vector<ScrfdCandidate> decode_scrfd_candidates(const std::vector<zhulong::Tensor>& outputs,
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
    std::priority_queue<ScrfdCandidate, std::vector<ScrfdCandidate>, ScrfdCandidateMinScoreFirst> top_candidates;

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
                    ScrfdCandidate candidate;
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

    std::vector<ScrfdCandidate> candidates;
    candidates.reserve(top_candidates.size());
    while (!top_candidates.empty()) {
        candidates.push_back(top_candidates.top());
        top_candidates.pop();
    }

    std::sort(candidates.begin(), candidates.end(),
              [](const ScrfdCandidate& a, const ScrfdCandidate& b) { return a.score > b.score; });

    std::vector<bool> suppressed(candidates.size(), false);
    std::vector<ScrfdCandidate> kept;
    kept.reserve(candidates.size());
    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) {
            continue;
        }
        kept.push_back(candidates[i]);
        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (!suppressed[j] && scrfd_iou(candidates[i], candidates[j]) > nms_threshold) {
                suppressed[j] = true;
            }
        }
    }
    return kept;
}

} // namespace zhulong::face
