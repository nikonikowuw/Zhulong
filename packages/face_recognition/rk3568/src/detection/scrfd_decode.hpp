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
#include <cstdint>
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
    uint32_t seq{0};                                  /**< 全网格确定性扫描序号（越小表示越早扫到） */
};

/**
 * @brief 解码预算
 *
 * 两块预算解决两个不同的问题，混成一个参数就会互相牵制：
 *   - `max_pre_nms_candidates`：全网格扫描保留多少候选进入 NMS（防单人密集重复 anchor
 *     把场景里其他人脸挤出预算 —— 旧实现「扫满就 break」会稳定丢掉近景大脸）。
 *   - `max_detections`：NMS 之后最多输出多少个目标（硬控下游每帧 512 维特征提取次数）。
 */
struct ScrfdDecodeLimits {
    size_t max_pre_nms_candidates{128};
    size_t max_detections{128};
};

/**
 * @brief letterbox 逆变换参数：把网格坐标映射回原帧像素
 *
 * 5 个标量收进聚合体，避免 `(orig_w, orig_h, det_scale, pad_left, pad_top)` 这种
 * 「三个 int 一个 float 混排」在调用点错位（错位不崩，只是框恒偏一个方向）。
 */
struct ScrfdLetterbox {
    float det_scale{1.0f}; /**< letterbox 缩放比（原帧 → 模型输入） */
    int pad_left{0};        /**< 居中补边左偏移（模型输入像素） */
    int pad_top{0};         /**< 居中补边上偏移（模型输入像素） */
    int orig_width{0};      /**< 原帧宽度 */
    int orig_height{0};     /**< 原帧高度 */
};

/**
 * @brief 严格全序：`a` 是否排在 `b` 前面（`score` 降序；同分按扫描序升序）
 *
 * `seq` 在全网格里唯一，因此这是一个**严格全序**而不是偏序：等分候选、
 * 超预算淘汰、最终排序、NMS 遍历顺序全部由它一次性钉死，`std::sort` 不会因
 * 实现细节抖动（旧实现用 `std::sort` + 仅按分数比较，等分输入顺序未定义）。
 */
inline bool scrfd_ranks_before(const ScrfdCandidate& a, const ScrfdCandidate& b) noexcept {
    return (a.score != b.score) ? (a.score > b.score) : (a.seq < b.seq);
}

/**
 * @brief 第一趟只排序用的轻量键：分数 + 扫描序 + 位置
 *
 * 不在这一趟算几何是有意的：`box` 与 5 个关键点的 letterbox 逆变换 + 钳位是解码里
 * 最贵的一段，而它只对最终留下的 ≤`max_pre_nms_candidates` 个候选有意义。
 */
struct ScrfdRankedAnchor {
    float score{0.0f};
    uint32_t seq{0};
    uint32_t layer{0};
    size_t index{0}; /**< 层内展平 anchor 下标 */
};

/** @brief 与 `scrfd_ranks_before` 同序，用于第一趟的定长堆 */
inline bool scrfd_anchor_ranks_before(const ScrfdRankedAnchor& a, const ScrfdRankedAnchor& b) noexcept {
    return (a.score != b.score) ? (a.score > b.score) : (a.seq < b.seq);
}

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
 * @brief 把一个 anchor 的回归输出还原成原帧像素框与关键点
 *
 * @param bboxes 该层的 bbox 输出（每 anchor 4 值，相对中心的 stride 倍数）
 * @param kpss 该层的 5 点输出（每 anchor 10 值，相对中心的 stride 倍数）
 * @param index 层内展平 anchor 下标 `(h * grid_w + w) * anchors_per_cell + a`
 * @return false 表示该 anchor 几何非法（非有限或退化框），调用方丢弃
 */
inline bool scrfd_decode_anchor(const float* bboxes,
                                const float* kpss,
                                size_t index,
                                int grid_w,
                                int stride,
                                int anchors_per_cell,
                                const ScrfdLetterbox& letterbox,
                                ScrfdCandidate& out) noexcept {
    if (bboxes == nullptr || kpss == nullptr || grid_w <= 0 || stride <= 0 || anchors_per_cell <= 0) {
        return false;
    }

    const size_t cell = index / static_cast<size_t>(anchors_per_cell);
    const float center_x = static_cast<float>(static_cast<int>(cell % static_cast<size_t>(grid_w)) * stride);
    const float center_y = static_cast<float>(static_cast<int>(cell / static_cast<size_t>(grid_w)) * stride);
    const float pad_x = static_cast<float>(letterbox.pad_left);
    const float pad_y = static_cast<float>(letterbox.pad_top);
    const float clamp_w = static_cast<float>(letterbox.orig_width);
    const float clamp_h = static_cast<float>(letterbox.orig_height);

    // 还原并扣除 Letterbox 居中补边与缩放比，映射回原帧像素坐标
    out.box[0] = std::clamp((center_x - bboxes[index * kScrfdBoxValuesPerAnchor + 0] * stride - pad_x) / letterbox.det_scale,
                            0.0f, clamp_w);
    out.box[1] = std::clamp((center_y - bboxes[index * kScrfdBoxValuesPerAnchor + 1] * stride - pad_y) / letterbox.det_scale,
                            0.0f, clamp_h);
    out.box[2] = std::clamp((center_x + bboxes[index * kScrfdBoxValuesPerAnchor + 2] * stride - pad_x) / letterbox.det_scale,
                            0.0f, clamp_w);
    out.box[3] = std::clamp((center_y + bboxes[index * kScrfdBoxValuesPerAnchor + 3] * stride - pad_y) / letterbox.det_scale,
                            0.0f, clamp_h);

    if (!std::isfinite(out.box[0]) || !std::isfinite(out.box[1]) ||
        !std::isfinite(out.box[2]) || !std::isfinite(out.box[3]) ||
        out.box[2] <= out.box[0] || out.box[3] <= out.box[1]) {
        return false;
    }

    for (size_t k = 0; k < kLandmarkCount; ++k) {
        const float kx = (center_x + kpss[index * kScrfdKpsValuesPerAnchor + k * 2 + 0] * stride - pad_x) / letterbox.det_scale;
        const float ky = (center_y + kpss[index * kScrfdKpsValuesPerAnchor + k * 2 + 1] * stride - pad_y) / letterbox.det_scale;
        if (!std::isfinite(kx) || !std::isfinite(ky)) {
            return false;
        }
        out.kps[k].x = std::clamp(kx, 0.0f, clamp_w);
        out.kps[k].y = std::clamp(ky, 0.0f, clamp_h);
    }
    return true;
}

/**
 * @brief 按 stride 表解码 SCRFD 三组输出为候选框，并做有界确定性 NMS
 *
 * 输出张量布局：前 N 个为 score、接着 N 个为 bbox、最后 N 个为 kps（N = stride 层数）。
 *
 * 两趟扫描：
 *   1. 全网格只读 `score`，把 `(score, seq, 位置)` 压进定长堆。不碰 bbox/kps —— 病态
 *      密集帧里 1.2 万个 anchor 全过阈值时，旧实现会为其中 96% 的候选白算一遍几何。
 *   2. 只对堆里留下的 ≤`max_pre_nms_candidates` 个候选做 letterbox 逆变换，再 NMS。
 *
 * 预算按「进入第二趟的候选数」计：几何非法（非有限/退化框）的候选会占掉一个预算名额
 * 并在第二趟被丢弃，不反向回补 —— 预算同时是**内存上界**，回补就要保留全量候选。
 *
 * @return 抑制后按 `(score 降序, seq 升序)` 确定性排列的候选；布局不符、参数非法或全部低于阈值时返回空
 */
template<size_t N>
inline std::vector<ScrfdCandidate> decode_scrfd_candidates(
    const std::vector<zhulong::Tensor>& outputs,
    const std::array<int, N>& strides,
    int anchors_per_cell,
    int model_width,
    int model_height,
    float score_threshold,
    float nms_threshold,
    const ScrfdDecodeLimits& limits,
    const ScrfdLetterbox& letterbox) {
    if (!std::isfinite(letterbox.det_scale) || letterbox.det_scale <= 0.0f ||
        !std::isfinite(score_threshold) || !std::isfinite(nms_threshold) ||
        letterbox.orig_width <= 0 || letterbox.orig_height <= 0 ||
        model_width <= 0 || model_height <= 0 ||
        limits.max_pre_nms_candidates == 0 || limits.max_detections == 0 ||
        !scrfd_outputs_match(outputs, strides, anchors_per_cell, model_width, model_height)) {
        return {};
    }

    std::array<const float*, N> layer_scores{};
    std::array<const float*, N> layer_boxes{};
    std::array<const float*, N> layer_kps{};
    for (size_t idx = 0; idx < N; ++idx) {
        layer_scores[idx] = outputs[idx].data<float>();
        layer_boxes[idx] = outputs[idx + N].data<float>();
        layer_kps[idx] = outputs[idx + 2 * N].data<float>();
        if (layer_scores[idx] == nullptr || layer_boxes[idx] == nullptr || layer_kps[idx] == nullptr) {
            return {};
        }
    }

    // 第一趟：全网格扫描，定长堆按 `(score 降序, seq 升序)` 保留前 `max_pre_nms_candidates`。
    // 堆比较器用「排在前面者更小」，于是 `front()` 恒为**排行最末**的那条，便于超预算时顶替。
    std::vector<ScrfdRankedAnchor> ranked;
    ranked.reserve(limits.max_pre_nms_candidates);
    uint32_t next_seq = 0;

    for (size_t idx = 0; idx < N; ++idx) {
        const int stride = strides[idx];
        const int grid_h = model_height / stride;
        const int grid_w = model_width / stride;
        const float* scores = layer_scores[idx];

        for (int h = 0; h < grid_h; ++h) {
            for (int w = 0; w < grid_w; ++w) {
                for (int a = 0; a < anchors_per_cell; ++a) {
                    const size_t index =
                        (static_cast<size_t>(h) * static_cast<size_t>(grid_w) + static_cast<size_t>(w)) *
                            static_cast<size_t>(anchors_per_cell) +
                        static_cast<size_t>(a);
                    const float score = scores[index]; // score 已含 Sigmoid，无需重复计算
                    if (!std::isfinite(score) || !(score >= score_threshold)) {
                        continue;
                    }

                    const ScrfdRankedAnchor entry{score, next_seq++, static_cast<uint32_t>(idx), index};
                    if (ranked.size() < limits.max_pre_nms_candidates) {
                        ranked.push_back(entry);
                        std::push_heap(ranked.begin(), ranked.end(), scrfd_anchor_ranks_before);
                    } else if (scrfd_anchor_ranks_before(entry, ranked.front())) {
                        std::pop_heap(ranked.begin(), ranked.end(), scrfd_anchor_ranks_before);
                        ranked.back() = entry;
                        std::push_heap(ranked.begin(), ranked.end(), scrfd_anchor_ranks_before);
                    }
                }
            }
        }
    }

    if (ranked.empty()) {
        return {};
    }

    // 第二趟：只对幸存者算几何（`(score 降序, seq 升序)` 已是严格全序，排序不需要 stable）
    std::sort(ranked.begin(), ranked.end(), scrfd_anchor_ranks_before);

    std::vector<ScrfdCandidate> candidates;
    candidates.reserve(ranked.size());
    for (const auto& anchor : ranked) {
        ScrfdCandidate candidate;
        const int stride = strides[anchor.layer];
        if (!scrfd_decode_anchor(layer_boxes[anchor.layer], layer_kps[anchor.layer],
                                 anchor.index, model_width / stride, stride, anchors_per_cell,
                                 letterbox, candidate)) {
            continue;
        }
        candidate.score = anchor.score;
        candidate.seq = anchor.seq;
        candidates.push_back(candidate);
    }

    std::vector<bool> suppressed(candidates.size(), false);
    std::vector<ScrfdCandidate> kept;
    kept.reserve(std::min(candidates.size(), limits.max_detections));
    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) {
            continue;
        }
        kept.push_back(candidates[i]);
        if (kept.size() >= limits.max_detections) {
            break;
        }
        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (!suppressed[j] && scrfd_iou(candidates[i], candidates[j]) > nms_threshold) {
                suppressed[j] = true;
            }
        }
    }
    return kept;
}

} // namespace zhulong::face
