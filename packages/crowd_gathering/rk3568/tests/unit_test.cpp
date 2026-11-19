/**
 * @file unit_test.cpp
 * @brief 包内自测：配置解析、透视自适应聚类几何、时序防抖状态机
 *
 * 归属约定（见 `.trellis/spec/native/algo-plugin-contract.md` §5.5）：
 *   - **包内自测**：不需要模型与会话的纯逻辑 → 留在包内 `tests/`，由包自己的
 *     `make unit-test` 驱动；第三方拿到「SDK 前缀 + 包目录」就能跑。
 *   - **黑盒验包**：`<包根可执行文件> --verify`（ABI 生命周期）与 `make test`（真实权重出图）
 *     由 CMake 的另外两个 target 承担。
 *
 * ⚠️ 刻意**不用** `assert()`：本包默认 `RelWithDebInfo` 构建会定义 NDEBUG，断言整段
 *    被编译掉 —— 曾经因此让「单测通过」变成一句无条件打印的空话（`nm | grep __assert_fail`
 *    为 0 即证据）。这里用带上下文的显式检查 + 非零退出码。
 */

#include "analysis/spatial_clustering.hpp"
#include "analysis/temporal_tracker.hpp"
#include "common/crowd_config.hpp"

#include <chrono>
#include <cmath>
#include <cstdio>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

void check(bool condition, const std::string& what) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %s\n", what.c_str());
        ++g_failures;
    }
}

bool nearly_equal(float a, float b, float tolerance) {
    return std::fabs(a - b) <= tolerance;
}

/** @brief 造一个归一化人体框：给接地点与身高（均为归一化值），宽度取身高的 40% */
zhulong::crowd::DetectedPerson make_person(float foot_x, float foot_y, float height, float score = 0.9f) {
    const float half_width = height * 0.2f;
    zhulong::crowd::DetectedPerson person;
    person.box = {foot_x - half_width, foot_y - height, foot_x + half_width, foot_y};
    person.score = score;
    return person;
}

// ---------------------------------------------------------------------------
// 1. 配置解析
// ---------------------------------------------------------------------------
void test_config_parsing() {
    using namespace zhulong::crowd;

    const CrowdConfig defaults{};
    check(nearly_equal(defaults.score_threshold, kDefaultScoreThreshold, 1e-6f), "config: default score threshold");
    check(defaults.gather_count_threshold == kDefaultGatherCountThreshold, "config: default gather count");

    const CrowdConfig parsed = resolve_crowd_config(
        R"({"score_threshold": 0.45, "gather_count_threshold": 4, "gather_distance_ratio": 2.0,
            "gather_duration_seconds": 12.5, "report_person_boxes": true})",
        defaults);
    check(nearly_equal(parsed.score_threshold, 0.45f, 1e-4f), "config: score_threshold override");
    check(parsed.gather_count_threshold == 4, "config: gather_count_threshold override");
    check(nearly_equal(parsed.gather_distance_ratio, 2.0f, 1e-4f), "config: gather_distance_ratio override");
    check(nearly_equal(parsed.gather_duration_seconds, 12.5f, 1e-4f), "config: gather_duration_seconds override");
    check(parsed.report_person_boxes, "config: report_person_boxes override");

    // 空视图 = 未下发 → 原样返回 fallback
    const CrowdConfig empty = resolve_crowd_config(std::string_view{}, parsed);
    check(empty.gather_count_threshold == 4, "config: empty view keeps fallback");

    // 越界值回落（schema 区间外的输入，宿主本会拒绝；离线调用时不能炸出诡异行为）
    const CrowdConfig clamped = resolve_crowd_config(
        R"({"score_threshold": 0.01, "gather_count_threshold": 999,
            "gather_distance_ratio": 99.0, "gather_duration_seconds": 9999.0})",
        defaults);
    check(nearly_equal(clamped.score_threshold, kDefaultScoreThreshold, 1e-6f), "config: score below range falls back");
    check(clamped.gather_count_threshold == kDefaultGatherCountThreshold, "config: count above range falls back");
    check(nearly_equal(clamped.gather_distance_ratio, kDefaultGatherDistanceRatio, 1e-6f),
          "config: distance ratio above range falls back");
    check(nearly_equal(clamped.gather_duration_seconds, kDefaultGatherDurationSeconds, 1e-6f),
          "config: duration above range falls back");

    // 假键：值里出现的同名子串不能被当成键
    const CrowdConfig decoys = resolve_crowd_config(
        R"({"note": "score_threshold:0.9", "nested": {"gather_count_threshold": 9}})",
        defaults);
    check(nearly_equal(decoys.score_threshold, kDefaultScoreThreshold, 1e-6f), "config: value text is not a key");
    check(decoys.gather_count_threshold == kDefaultGatherCountThreshold,
          "config: nested object key is not taken from the top level");

    // 非数字尾巴不能被 stod 读成前缀
    const CrowdConfig garbage = resolve_crowd_config(R"({"gather_count_threshold": 7x})", defaults);
    check(garbage.gather_count_threshold == kDefaultGatherCountThreshold, "config: trailing garbage rejected");

    // 布尔假值
    const CrowdConfig false_flag = resolve_crowd_config(R"({"report_person_boxes": false})", CrowdConfig{true});
    check(!false_flag.report_person_boxes, "config: report_person_boxes false override");
}

// ---------------------------------------------------------------------------
// 2. 检测输入分辨率校验
// ---------------------------------------------------------------------------
void test_detector_input_size() {
    using namespace zhulong::crowd;

    check(is_valid_detector_input_size(640, 640), "input size: 640x640 valid");
    check(is_valid_detector_input_size(640, 384), "input size: 640x384 valid (16:9 cctv preset)");
    check(!is_valid_detector_input_size(600, 640), "input size: width not divisible by 128 rejected");
    check(!is_valid_detector_input_size(640, 400), "input size: height not divisible by 128 rejected");
    check(!is_valid_detector_input_size(0, 0), "input size: zero rejected");
    check(!is_valid_detector_input_size(4096, 4096), "input size: oversized rejected");

    const DetectorInputSize defaults{};
    // 使用场景是 CCTV（16:9）：默认档位必须与方形 640x640 的内容分辨率一致但更省算力
    check(defaults.width == 640 && defaults.height == 384, "input size: default is the 640x384 cctv preset");
    check(is_valid_detector_input_size(defaults.width, defaults.height), "input size: default preset is valid");

    const DetectorInputSize overridden = resolve_detector_input_size(
        R"({"detect_input_width": 640, "detect_input_height": 512})", defaults);
    check(overridden.width == 640 && overridden.height == 512, "input size: override applied");
    check(!overridden.rejected, "input size: override not flagged as rejected");

    const DetectorInputSize invalid = resolve_detector_input_size(
        R"({"detect_input_width": 601, "detect_input_height": 384})", defaults);
    check(invalid.width == defaults.width && invalid.height == defaults.height,
          "input size: invalid pair falls back as a whole");
    check(invalid.rejected, "input size: invalid pair is reported to the caller");
}

// ---------------------------------------------------------------------------
// 3. 空间聚类几何
// ---------------------------------------------------------------------------
void test_spatial_clustering() {
    using namespace zhulong::crowd;

    // (a) 三人贴近、一人远离 → 只出一个 3 人簇
    //     身高 0.5、相邻 0.05 → 相对距离 0.1；远处那位相距 0.60 → 1.2 倍，必须落在阈值（1.0）外
    std::vector<DetectedPerson> crowd;
    crowd.push_back(make_person(0.20f, 1.00f, 0.50f));
    crowd.push_back(make_person(0.25f, 1.00f, 0.50f));
    crowd.push_back(make_person(0.30f, 1.00f, 0.50f));
    crowd.push_back(make_person(0.90f, 1.00f, 0.50f));

    const auto clusters = SpatialClusterer::cluster(crowd, 1.0f, 3, 1.0f);
    check(clusters.size() == 1, "cluster: exactly one cluster");
    if (!clusters.empty()) {
        check(clusters[0].person_count == 3, "cluster: three members (distant person excluded)");
        check(clusters[0].enclosing_box[0] <= 0.16f && clusters[0].enclosing_box[2] >= 0.34f,
              "cluster: enclosing box covers members");
    }
    check(SpatialClusterer::cluster(crowd, 1.0f, 4, 1.0f).empty(), "cluster: count threshold respected");
    check(SpatialClusterer::cluster(crowd, 1.0f, 1, 1.0f).empty(), "cluster: count threshold <= 1 is refused");

    // (b) aspect 不变性：1920x1080 帧，两人身高 200px（归一化 0.1852），接地点横向相距 300px。
    //     像素空间相对距离 = 300 / 200 = 1.5 > 1.2 → **不应**成簇。
    //     若忽略 aspect（旧行为），归一化距离 300/1920 = 0.1563 除以 0.1852 只有 0.84，
    //     会被错误地判成「同一簇」—— 横向判据被放宽 (1920/1080) 倍。
    constexpr float kWideAspect = 1920.0f / 1080.0f;
    const float height = 200.0f / 1080.0f;
    const float foot_y = 0.95f;
    std::vector<DetectedPerson> horizontally_spread;
    horizontally_spread.push_back(make_person(0.30f, foot_y, height));
    horizontally_spread.push_back(make_person(0.30f + 300.0f / 1920.0f, foot_y, height));

    check(SpatialClusterer::cluster(horizontally_spread, 1.2f, 2, kWideAspect).empty(),
          "cluster: aspect-aware horizontal distance keeps 1.5x separated people apart");
    check(SpatialClusterer::cluster(horizontally_spread, 1.2f, 2, 1.0f).size() == 1,
          "cluster: same pair would merge without aspect correction (regression guard for the anisotropy bug)");

    // (c) 同样两排人横向只差 200px（1.0 倍身高）时应成簇
    std::vector<DetectedPerson> horizontal_close;
    horizontal_close.push_back(make_person(0.30f, foot_y, height));
    horizontal_close.push_back(make_person(0.30f + 200.0f / 1920.0f, foot_y, height));
    check(SpatialClusterer::cluster(horizontal_close, 1.2f, 2, kWideAspect).size() == 1,
          "cluster: 1.0x height gap still clusters");

    // (d) 纵向（前后排）不受 aspect 影响：相距 300px 的两排同样应分开
    std::vector<DetectedPerson> vertically_spread;
    vertically_spread.push_back(make_person(0.40f, 0.95f, height));
    vertically_spread.push_back(make_person(0.40f, 0.95f - 300.0f / 1080.0f, height));
    check(SpatialClusterer::cluster(vertically_spread, 1.2f, 2, kWideAspect).empty(),
          "cluster: vertical distance above threshold stays apart");

    // (e) 非法 aspect / 非法距离比 → 保守返回，不产生 NaN 粘连
    check(SpatialClusterer::cluster(crowd, 1.0f, 3, 0.0f).size() == 1, "cluster: zero aspect degrades to 1.0");
    check(SpatialClusterer::cluster(crowd, 0.0f, 3, 1.0f).empty(), "cluster: non-positive distance ratio refused");
}

// ---------------------------------------------------------------------------
// 4. 时序防抖
// ---------------------------------------------------------------------------
std::vector<zhulong::crowd::ClusterCandidate> make_candidate(float center_x, int person_count) {
    zhulong::crowd::ClusterCandidate candidate;
    candidate.enclosing_box = {center_x - 0.1f, 0.5f, center_x + 0.1f, 0.9f};
    candidate.centroid_x = center_x;
    candidate.centroid_y = 0.7f;
    candidate.person_count = person_count;
    candidate.score = 0.8f;
    return {candidate};
}

void test_temporal_tracker() {
    using namespace zhulong::crowd;
    using clock = std::chrono::steady_clock;

    const auto now = clock::now();
    const auto candidates = make_candidate(0.5f, 3);

    // (a) 未满时长不告警，跨过阈值后告警
    TaskStreamTracker tracker;
    check(tracker.update(candidates, 1.0f, now).empty(), "tracker: first frame below dwell threshold");
    const auto alarms = tracker.update(candidates, 1.0f, now + std::chrono::milliseconds(1100));
    check(alarms.size() == 1, "tracker: alarm after dwell threshold");
    if (!alarms.empty()) {
        check(alarms[0].person_count == 3, "tracker: alarm carries person count");
        check(alarms[0].dwell_seconds >= 1.0f, "tracker: alarm carries dwell seconds");
    }

    // (b) 时长为 0 → 单帧命中即告警
    TaskStreamTracker immediate;
    const auto instant = immediate.update(candidates, 0.0f, now);
    check(instant.size() == 1, "tracker: zero duration alarms on the first frame");

    // (c) 宽限期随滞留时长伸展：60s 阈值下，3s 的空帧（人群短暂走位）不该清零滞留积分
    //     ⚠️ 空帧必须真的「候选为空」：只有在没匹配上的帧里，宽限期才会决定 track 的死活。
    TaskStreamTracker long_dwell;
    long_dwell.update(candidates, 60.0f, now);
    long_dwell.update({}, 60.0f, now + std::chrono::seconds(3));
    check(long_dwell.track_count() == 1, "tracker: 3s gap keeps tracking under 60s dwell (scaled grace)");
    const auto long_alarms = long_dwell.update(candidates, 60.0f, now + std::chrono::seconds(62));
    check(long_alarms.size() == 1, "tracker: dwell accumulates across a gap when duration is long");
    if (!long_alarms.empty()) {
        check(long_alarms[0].dwell_seconds >= 60.0f, "tracker: accumulated dwell reaches the threshold");
    }

    // (d) 簇消失超过宽限期后 track 被淘汰
    TaskStreamTracker evicting;
    evicting.update(candidates, 1.0f, now);
    check(evicting.track_count() == 1, "tracker: track created");
    evicting.update({}, 1.0f, now + std::chrono::seconds(5));
    check(evicting.track_count() == 0, "tracker: stale track evicted after grace period");

    // (e) 两路互不干扰
    TemporalTracker per_task;
    check(per_task.process(1, candidates, 5.0f).empty(), "tracker: task 1 first frame");
    check(per_task.process(2, candidates, 5.0f).empty(), "tracker: task 2 independent state");

    // (f) 空候选不会制造告警
    TaskStreamTracker idle;
    check(idle.update({}, 0.0f, now).empty(), "tracker: empty candidates never alarm");

    // (g) 幽灵告警防护：触发告警后，单帧漏检（候选为空）在宽限期内绝不上报历史旧框！
    TaskStreamTracker ghost_guard;
    ghost_guard.update(candidates, 1.0f, now);
    const auto g_alarmed = ghost_guard.update(candidates, 1.0f, now + std::chrono::milliseconds(1100));
    check(g_alarmed.size() == 1, "ghost_guard: triggered alarm");
    // 下一帧画面人群短暂遮挡/漏检（候选为空）
    const auto g_miss = ghost_guard.update({}, 1.0f, now + std::chrono::milliseconds(1500));
    check(g_miss.empty(), "ghost_guard: no ghost alarms emitted on empty frame during grace period");
    check(ghost_guard.track_count() == 1, "ghost_guard: track kept alive in grace period");
    // 下一帧重新被检测到：恢复告警，且滞留时长累积（由 first_seen 维持）
    const auto g_recovered = ghost_guard.update(candidates, 1.0f, now + std::chrono::milliseconds(2000));
    check(g_recovered.size() == 1, "ghost_guard: alarm resumes when crowd reappears");
    if (!g_recovered.empty()) {
        check(g_recovered[0].dwell_seconds >= 2.0f, "ghost_guard: dwell time accumulated properly");
    }

    // (h) 全局最高分优先匹配：候选 0 微弱匹配，候选 1 极高匹配 → 候选 1 必须成功绑定 track
    TaskStreamTracker match_prio;
    match_prio.update(make_candidate(0.5f, 3), 1.0f, now); // track 位于 center_x = 0.5
    // 候选 0 在 0.62（IoU 极低/边缘），候选 1 在 0.51（IoU 极高）
    const auto cand_weak = make_candidate(0.62f, 3)[0];
    const auto cand_strong = make_candidate(0.51f, 3)[0];
    std::vector<zhulong::crowd::ClusterCandidate> mixed{cand_weak, cand_strong};
    match_prio.update(mixed, 1.0f, now + std::chrono::milliseconds(200));
    // 检查 track_count：有 1 个被绑定，1 个变为新 track，总数为 2
    check(match_prio.track_count() == 2, "match_prio: strong candidate matches existing track, weak becomes new");
}

} // namespace

int main() {
    test_config_parsing();
    test_detector_input_size();
    test_spatial_clustering();
    test_temporal_tracker();

    if (g_failures != 0) {
        std::fprintf(stderr, "\n[✗] crowd gathering unit tests: %d failure(s)\n", g_failures);
        return 1;
    }
    std::printf("[✓] crowd gathering unit tests passed\n");
    return 0;
}
