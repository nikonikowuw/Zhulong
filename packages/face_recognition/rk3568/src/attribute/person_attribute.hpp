/**
 * @file person_attribute.hpp
 * @brief 人体属性（PPLCNet_x1_0 / PULC person_attribute）的模型无关逻辑
 *
 * 这里只有**不碰模型**的部分：标签表与逐标签阈值、26 维输出到属性的筛选排序、
 * 采样缓存（按框 IoU 关联的轻量轨迹）、配置解析。真正的前处理与推理在
 * `person_attribute.cpp`（逐档位不同：onnx 需要 NCHW float 归一化，rknn 由 NPU
 * 自带 mean/std，直接喂 HWC uint8）。
 *
 * 为什么采样缓存是必需的：人体属性模型是**单目标**分类器（输入 256×192 的
 * 单人裁剪），NPU 上一人一次 14.86 ms（FP16，RK3568）。监控帧率下逐帧给画面里
 * 每个人各跑一次，成本随人数线性膨胀 —— 所以按轨迹缓存属性、只对「新出现或
 * 超过刷新周期的人」重新分类。
 *
 * 轨迹键（`PersonAttributeTrack::id`）只在包内使用，**绝不**写入 `track_id`：
 * 宿主独占人员跟踪身份（宿主会用 `debugIgnoredTrackID` 明确忽略插件的 track_id），
 * 而 `IsFaceCloseup()` 又要求人体项的 `track_id` 恒为 0。
 */

#pragma once

#include "common/match_config.hpp"

#include <zhulong/algo.hpp>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <string>
#include <string_view>
#include <vector>

namespace zhulong::face {

/** @brief 属性分类模型输入尺寸（与 PULC 训练/导出的静态输入一致） */
inline constexpr int kPersonAttributeInputWidth = 192;
inline constexpr int kPersonAttributeInputHeight = 256;
/** @brief 模型输出维度（26 个相互独立的 sigmoid 概率，不是 softmax） */
inline constexpr int kPersonAttributeCount = 26;
/**
 * @brief 每人最多输出的属性条数
 *
 * 单帧人体上限 128、缓存轨道上限 64；若每人把 26 个标签全带上，属性 JSON 可能把
 * 整条结果顶到 64KB IPC 载荷门禁（超限时宿主整帧丢弃）。按分数取前 8 个既覆盖常用
 * 标签组合，也把最坏情况压到约 15KB。
 */
inline constexpr std::size_t kMaxPersonAttributesPerPerson = 8;

/** @brief 标签顺序必须与模型输出维度严格一致（顺序错了不会崩，只会全部错位） */
inline constexpr std::array<std::string_view, kPersonAttributeCount> kPersonAttributeLabels{
    "Hat",                // 0
    "Glasses",            // 1
    "ShortSleeve",        // 2
    "LongSleeve",         // 3
    "UpperStride",        // 4
    "UpperLogo",          // 5
    "UpperPlaid",         // 6
    "UpperSplice",        // 7
    "LowerStripe",        // 8
    "LowerPattern",       // 9
    "LongCoat",           // 10
    "Trousers",           // 11
    "Shorts",             // 12
    "Skirt&Dress",        // 13
    "Boots",              // 14
    "HandBag",            // 15
    "ShoulderBag",        // 16
    "Backpack",           // 17
    "HoldObjectsInFront", // 18
    "AgeLess18",          // 19
    "Age18-60",           // 20
    "AgeOver60",          // 21
    "Female",             // 22
    "Front",              // 23
    "Side",               // 24
    "Back",               // 25
};

/** @brief 逐标签判定阈值默认值（与 PPLCNet 官方推理脚本一致：眼镜/持物单独放宽） */
inline constexpr std::array<float, kPersonAttributeCount> kPersonAttributeThresholds{
    0.50f, 0.30f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f,
    0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.60f, 0.50f,
    0.50f, 0.50f, 0.50f, 0.50f, 0.50f, 0.50f,
};

/** @brief 人体属性阶段运行参数（进程级默认值可被任务级 JSON 覆盖） */
struct PersonAttributeParams {
    /** @brief 是否启用人身属性阶段；关闭后不建会话、不推理、不输出该键 */
    bool enabled{true};
    /** @brief 同一轨迹重新分类的最小间隔（毫秒）；越大越省、属性越滞后 */
    int refresh_ms{1000};
    /** @brief 单帧最多分类的人数（按人体框面积降序取前 N，大目标优先） */
    int max_persons_per_frame{2};
};

/** @brief 采样参数默认值与合法域（与 config.schema.json 两档位同步） */
inline constexpr int kDefaultPersonAttributeRefreshMs = 1000;
inline constexpr int kMinPersonAttributeRefreshMs = 200;
inline constexpr int kMaxPersonAttributeRefreshMs = 10000;
inline constexpr int kDefaultPersonAttributeMaxPersons = 2;
inline constexpr int kMinPersonAttributeMaxPersons = 1;
inline constexpr int kMaxPersonAttributeMaxPersons = 8;

/** @brief 属性轨迹在画面中消失多久后淘汰：至少保留 3 个刷新周期，且不短于 5 秒 */
inline constexpr int kPersonAttributeTrackTtlFactor = 3;
inline constexpr int kPersonAttributeTrackTtlFloorMs = 5000;
/** @brief 单 worker 的轨迹缓存上限（超出时淘汰最久未见的，防止长时运行内存漂移） */
inline constexpr size_t kMaxPersonAttributeTracks = 64;
/** @brief 框关联 IoU 下限（低于它视为新人；人群密集时宁可多跑一次分类也不要串属性） */
inline constexpr float kPersonAttributeMatchIou = 0.30f;

/**
 * @brief 把 26 维模型输出筛选成属性列表
 *
 * @param scores 模型原始输出（sigmoid 已在图内完成，这里**不再**做激活）
 * @param count  输出维度，必须等于 `kPersonAttributeCount`
 * @return 分数不低于该标签阈值的属性，按分数降序；无达标项时为空
 */
[[nodiscard]] inline std::vector<zhulong::PersonAttribute> extract_person_attributes(
    const float* scores, size_t count
) {
    std::vector<zhulong::PersonAttribute> attributes;
    if (scores == nullptr || count != static_cast<size_t>(kPersonAttributeCount)) {
        return attributes;
    }
    attributes.reserve(kPersonAttributeCount);
    for (size_t i = 0; i < count; ++i) {
        const float score = scores[i];
        if (!std::isfinite(score) || score < kPersonAttributeThresholds[i] || score > 1.0f) {
            continue;
        }
        attributes.push_back(zhulong::PersonAttribute{std::string(kPersonAttributeLabels[i]), score});
    }
    std::stable_sort(attributes.begin(), attributes.end(),
                     [](const zhulong::PersonAttribute& lhs, const zhulong::PersonAttribute& rhs) {
                         return lhs.score > rhs.score;
                     });
    if (attributes.size() > kMaxPersonAttributesPerPerson) {
        attributes.resize(kMaxPersonAttributesPerPerson);
    }
    return attributes;
}

/** @brief 归一化框 [x1, y1, x2, y2] 的交并比；任一框退化时返回 0（不参与关联） */
[[nodiscard]] inline float person_attribute_box_iou(
    const std::array<float, 4>& a, const std::array<float, 4>& b
) noexcept {
    const float w1 = a[2] - a[0];
    const float h1 = a[3] - a[1];
    const float w2 = b[2] - b[0];
    const float h2 = b[3] - b[1];
    if (!(w1 > 0.0f) || !(h1 > 0.0f) || !(w2 > 0.0f) || !(h2 > 0.0f)) {
        return 0.0f;
    }
    const float inter_w = std::min(a[2], b[2]) - std::max(a[0], b[0]);
    const float inter_h = std::min(a[3], b[3]) - std::max(a[1], b[1]);
    if (!(inter_w > 0.0f) || !(inter_h > 0.0f)) {
        return 0.0f;
    }
    const float inter = inter_w * inter_h;
    const float uni = w1 * h1 + w2 * h2 - inter;
    return uni > 0.0f ? inter / uni : 0.0f;
}

/** @brief 单条属性轨迹：包内采样缓存的键，不对外暴露 */
struct PersonAttributeTrack {
    uint64_t id{0};                                 /**< 包内稳定键（写回缓存时定位用，避免下标漂移） */
    std::array<float, 4> box{};                     /**< 最近一次关联到的归一化人体框 */
    std::vector<zhulong::PersonAttribute> attributes; /**< 最近一次分类结果（空 = 尚无结果） */
    int64_t last_seen_ms{0};                        /**< 最近一次被检出关联的时间 */
    int64_t updated_ms{0};                          /**< 最近一次分类完成的时间；0 表示从未分类 */
};

/** @brief 单个 worker 的属性轨迹缓存 */
struct WorkerAttributeCache {
    std::vector<PersonAttributeTrack> tracks;
    uint64_t next_track_id{1};
};

/**
 * @brief 把本帧人体框关联到缓存轨迹，未命中的框就地新建轨迹
 *
 * 贪心按 IoU 降序配对（一张框只配一条轨迹），保持「命中即刷新位置、未命中即新建」。
 * 属性不因框轻微漂移而丢失，也不会把两个人的属性互相串上。
 *
 * @return 与 `boxes` 等长的数组，元素为对应轨迹在 `tracks` 中的下标
 */
[[nodiscard]] inline std::vector<size_t> associate_person_tracks(
    WorkerAttributeCache& cache, const std::vector<std::array<float, 4>>& boxes, int64_t now_ms
) {
    std::vector<size_t> mapping(boxes.size(), static_cast<size_t>(-1));
    std::vector<bool> track_used(cache.tracks.size(), false);

    struct Match {
        float iou;
        size_t detection;
        size_t track;
    };
    std::vector<Match> matches;
    for (size_t d = 0; d < boxes.size(); ++d) {
        for (size_t t = 0; t < cache.tracks.size(); ++t) {
            const float iou = person_attribute_box_iou(boxes[d], cache.tracks[t].box);
            if (iou >= kPersonAttributeMatchIou) {
                matches.push_back(Match{iou, d, t});
            }
        }
    }
    std::stable_sort(matches.begin(), matches.end(),
                     [](const Match& lhs, const Match& rhs) { return lhs.iou > rhs.iou; });
    for (const Match& match : matches) {
        if (mapping[match.detection] != static_cast<size_t>(-1) || track_used[match.track]) {
            continue;
        }
        mapping[match.detection] = match.track;
        track_used[match.track] = true;
        cache.tracks[match.track].box = boxes[match.detection];
        cache.tracks[match.track].last_seen_ms = now_ms;
    }

    for (size_t d = 0; d < boxes.size(); ++d) {
        if (mapping[d] != static_cast<size_t>(-1)) {
            continue;
        }
        PersonAttributeTrack track;
        track.id = cache.next_track_id++;
        track.box = boxes[d];
        track.last_seen_ms = now_ms;
        track.updated_ms = 0;
        cache.tracks.push_back(std::move(track));
        mapping[d] = cache.tracks.size() - 1;
    }
    return mapping;
}

/** @brief 淘汰长时间未见的轨迹，并在超限时保留最近出现的若干条 */
inline void prune_person_attribute_tracks(
    WorkerAttributeCache& cache, int64_t now_ms, int refresh_ms
) {
    const int64_t ttl_ms = std::max<int64_t>(
        static_cast<int64_t>(refresh_ms) * kPersonAttributeTrackTtlFactor, kPersonAttributeTrackTtlFloorMs);
    cache.tracks.erase(
        std::remove_if(cache.tracks.begin(), cache.tracks.end(),
                       [now_ms, ttl_ms](const PersonAttributeTrack& track) {
                           return now_ms - track.last_seen_ms > ttl_ms;
                       }),
        cache.tracks.end());

    if (cache.tracks.size() <= kMaxPersonAttributeTracks) {
        return;
    }
    std::stable_sort(cache.tracks.begin(), cache.tracks.end(),
                     [](const PersonAttributeTrack& lhs, const PersonAttributeTrack& rhs) {
                         return lhs.last_seen_ms > rhs.last_seen_ms;
                     });
    cache.tracks.resize(kMaxPersonAttributeTracks);
}

/**
 * @brief 选出本帧需要（重新）分类的轨迹下标
 *
 * 「到期」= 从未分类过，或距上次分类已满刷新周期；按人体框面积降序，只取前
 * `max_count` 条 —— 画面里同时出现很多人时，先保证近景大目标的属性准确。
 */
[[nodiscard]] inline std::vector<size_t> select_tracks_for_classification(
    const WorkerAttributeCache& cache, int64_t now_ms, int refresh_ms, int max_count
) {
    std::vector<size_t> due;
    for (size_t i = 0; i < cache.tracks.size(); ++i) {
        const PersonAttributeTrack& track = cache.tracks[i];
        if (track.updated_ms != 0 && now_ms - track.updated_ms < refresh_ms) {
            continue;
        }
        due.push_back(i);
    }
    std::stable_sort(due.begin(), due.end(), [&cache](size_t lhs, size_t rhs) {
        const auto& a = cache.tracks[lhs].box;
        const auto& b = cache.tracks[rhs].box;
        const float area_a = (a[2] - a[0]) * (a[3] - a[1]);
        const float area_b = (b[2] - b[0]) * (b[3] - b[1]);
        return area_a > area_b;
    });
    if (max_count > 0 && due.size() > static_cast<size_t>(max_count)) {
        due.resize(static_cast<size_t>(max_count));
    }
    return due;
}

/**
 * @brief 在默认值之上叠加任务级 JSON 的覆盖（越界值回落默认，与 `resolve_match_params` 同语义）
 */
[[nodiscard]] inline PersonAttributeParams resolve_person_attribute_params(
    std::string_view task_json, PersonAttributeParams defaults
) {
    if (task_json.empty()) {
        return defaults;
    }
    PersonAttributeParams resolved = defaults;

    bool enabled = false;
    if (find_json_bool(task_json, "enable_person_attributes", enabled)) {
        resolved.enabled = enabled;
    }

    double refresh_ms = 0.0;
    if (find_json_number(task_json, "person_attribute_refresh_ms", refresh_ms) &&
        refresh_ms >= static_cast<double>(kMinPersonAttributeRefreshMs) &&
        refresh_ms <= static_cast<double>(kMaxPersonAttributeRefreshMs)) {
        resolved.refresh_ms = static_cast<int>(std::lround(refresh_ms));
    }

    double max_persons = 0.0;
    if (find_json_number(task_json, "person_attribute_max_persons", max_persons) &&
        max_persons >= static_cast<double>(kMinPersonAttributeMaxPersons) &&
        max_persons <= static_cast<double>(kMaxPersonAttributeMaxPersons)) {
        resolved.max_persons_per_frame = static_cast<int>(std::lround(max_persons));
    }

    return resolved;
}

/**
 * @brief 单人裁剪的人体属性分类器
 *
 * 只负责「给定原帧与一个归一化人体框，产出属性列表」；采样/缓存/关联由调用方
 * 用上面的自由函数完成，两者边界清晰：分类器是有模型的、不可单测的部分，采样
 * 逻辑是无模型的、被 unit-test 覆盖的部分。
 */
class PersonAttributeClassifier {
public:
    /**
     * @brief 对单个人体框跑一次属性分类
     *
     * @param session      本 worker 的属性分类会话
     * @param image        输入帧（只在本次调用期间有效）
     * @param body_box     归一化人体框 [x1, y1, x2, y2]
     * @param out_attributes 输出：过阈值的属性，按分数降序（失败时清空）
     * @return false 表示框退化、裁剪失败、模型输出形状/数值不合法（调用方按「本帧无结果」处理）
     */
    bool classify(zhulong::Session& session,
                  const zhulong::Image& image,
                  const std::array<float, 4>& body_box,
                  std::vector<zhulong::PersonAttribute>& out_attributes) const;
};

} // namespace zhulong::face
