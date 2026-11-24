#include "analysis/helmet_association.hpp"
#include "analysis/violation_tracker.hpp"
#include "common/ppe_config.hpp"
#include "detection/yolov8_ppe_decode.hpp"

#include <cmath>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << "\n";
        ++g_failures;
    }
}

bool nearly_equal(float a, float b, float epsilon = 1e-4f) {
    return std::fabs(a - b) <= epsilon;
}

zhulong::Detection make_detection(float x1, float y1, float x2, float y2, float score,
                                  int32_t class_id) {
    zhulong::Detection detection;
    detection.box[0] = x1;
    detection.box[1] = y1;
    detection.box[2] = x2;
    detection.box[3] = y2;
    detection.confidence = score;
    detection.class_id = class_id;
    detection.label = zhulong::ppe::ppe_class_label(class_id);
    return detection;
}

struct HeadFixture {
    int grid{0};
    std::vector<float> box;
    std::vector<float> score;
    zhulong::ppe::Yolov8PpeHeadView view;

    HeadFixture(int grid_size, int stride)
        : grid(grid_size),
          box(static_cast<size_t>(4 * zhulong::ppe::kDflBins) * static_cast<size_t>(grid_size) *
                  static_cast<size_t>(grid_size),
              -20.0f),
          score(static_cast<size_t>(zhulong::ppe::kClassCount) * static_cast<size_t>(grid_size) *
                    static_cast<size_t>(grid_size),
                0.0f) {
        view.box = box.data();
        view.score = score.data();
        view.grid_h = grid_size;
        view.grid_w = grid_size;
        view.stride_h = stride;
        view.stride_w = stride;
    }

    /**
     * @brief 把 anchor 配成「左/上距离=0，右/下距离=1 bin」的确定性 DFL（17 bin）
     */
    void set_anchor(int row, int col, int32_t class_id, float prob = 0.9f) {
        const size_t plane = static_cast<size_t>(grid) * static_cast<size_t>(grid);
        const size_t anchor = static_cast<size_t>(row) * static_cast<size_t>(grid) +
                              static_cast<size_t>(col);
        score[static_cast<size_t>(class_id) * plane + anchor] = prob;
        box[(0 * zhulong::ppe::kDflBins + 0) * plane + anchor] = 20.0f;
        box[(1 * zhulong::ppe::kDflBins + 0) * plane + anchor] = 20.0f;
        box[(2 * zhulong::ppe::kDflBins + 1) * plane + anchor] = 20.0f;
        box[(3 * zhulong::ppe::kDflBins + 1) * plane + anchor] = 20.0f;
    }
};

void test_config() {
    const auto defaults = zhulong::ppe::resolve_ppe_config("", zhulong::ppe::PpeConfig{});
    check(defaults.score_threshold == 0.40f, "default score_threshold");
    check(defaults.nms_threshold == 0.50f, "default nms_threshold");
    check(defaults.min_head_height_ratio == 0.02f, "default min_head_height_ratio");
    check(!defaults.report_compliant, "default report_compliant");
    check(defaults.confirm_frames == 2, "default confirm_frames");

    const auto custom = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.5,"nms_threshold":0.3,"min_head_height_ratio":0.05,)"
        R"("report_compliant":true,"confirm_frames":3})",
        zhulong::ppe::PpeConfig{});
    check(custom.score_threshold == 0.5f, "custom score_threshold");
    check(custom.nms_threshold == 0.3f, "custom nms_threshold");
    check(custom.min_head_height_ratio == 0.05f, "custom min_head_height_ratio");
    check(custom.report_compliant, "custom report_compliant");
    check(custom.confirm_frames == 3, "custom confirm_frames");

    const auto out_of_range = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.99,"nms_threshold":0.01,"min_head_height_ratio":0.9,)"
        R"("confirm_frames":2.5})",
        zhulong::ppe::PpeConfig{});
    check(out_of_range.score_threshold == 0.40f, "out-of-range score falls back");
    check(out_of_range.nms_threshold == 0.50f, "out-of-range nms falls back");
    check(out_of_range.min_head_height_ratio == 0.02f, "out-of-range height falls back");
    check(out_of_range.confirm_frames == 2, "non-integer confirm_frames falls back");

    const auto frames_high =
        zhulong::ppe::resolve_ppe_config(R"({"confirm_frames":11})", zhulong::ppe::PpeConfig{});
    check(frames_high.confirm_frames == 2, "confirm_frames above kMaxConfirmFrames falls back");

    const auto bool_type = zhulong::ppe::resolve_ppe_config(
        R"({"report_compliant":"true"})", zhulong::ppe::PpeConfig{});
    check(!bool_type.report_compliant, "non-literal bool falls back");

    const auto nested = zhulong::ppe::resolve_ppe_config(
        R"({"nested":{"score_threshold":0.8},"note":"score_threshold: 0.9"})",
        zhulong::ppe::PpeConfig{});
    check(nested.score_threshold == 0.40f, "nested/string keys must not be read");

    const auto malformed = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.4x})", zhulong::ppe::PpeConfig{});
    check(malformed.score_threshold == 0.40f, "trailing garbage must be rejected");
}

void test_class_labels() {
    check(std::string(zhulong::ppe::ppe_class_label(zhulong::ppe::kClassHeadHelmet)) ==
              "head_helmet",
          "label head_helmet");
    check(std::string(zhulong::ppe::ppe_class_label(zhulong::ppe::kClassHeadNoHelmet)) ==
              "head_nohelmet",
          "label head_nohelmet");
    check(std::string(zhulong::ppe::ppe_class_label(99)) == "object", "unknown class label");
}

void test_decode_head() {
    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassHeadNoHelmet, 0.875f);
    const zhulong::ppe::Yolov8PpeGeometry geometry{1.0f, 0, 0, 32, 32};
    zhulong::ppe::PpeDecodeBuffers buffers;

    zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, buffers);
    const auto& result = buffers.detections;
    check(result.size() == 1, "decode returns one head_nohelmet");
    if (result.size() == 1) {
        check(result[0].class_id == zhulong::ppe::kClassHeadNoHelmet, "decode class head_nohelmet");
        check(result[0].label == "head_nohelmet", "decode label head_nohelmet");
        check(nearly_equal(result[0].confidence, 0.875f), "decode direct prob score");
        check(nearly_equal(result[0].box[0], 0.25f), "decode x1");
        check(nearly_equal(result[0].box[1], 0.25f), "decode y1");
        check(nearly_equal(result[0].box[2], 0.50f), "decode x2");
        check(nearly_equal(result[0].box[3], 0.50f), "decode y2");
    }
}

void test_decode_buffers_reused() {
    const zhulong::ppe::Yolov8PpeGeometry geometry{1.0f, 0, 0, 32, 32};
    zhulong::ppe::PpeDecodeBuffers buffers;

    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassHeadNoHelmet, 0.9f);
    zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, buffers);
    check(buffers.detections.size() == 1, "first decode returns one");
    check(buffers.candidates.size() >= buffers.detections.size(), "candidates retained in buffers");

    zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, buffers);
    check(buffers.detections.size() == 1, "second decode does not accumulate detections");

    zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.95f, 0.45f, buffers);
    check(buffers.detections.empty(), "threshold above score yields no detections");
}

void test_decode_geometry() {
    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassHeadHelmet, 0.9f);
    const zhulong::ppe::Yolov8PpeGeometry geometry{2.0f, 0, 0, 100, 100};
    zhulong::ppe::PpeDecodeBuffers buffers;

    zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, buffers);
    const auto& result = buffers.detections;
    check(result.size() == 1, "decode geometry returns one");
    if (result.size() == 1) {
        check(nearly_equal(result[0].box[0], 0.04f), "geometry x1");
        check(nearly_equal(result[0].box[1], 0.04f), "geometry y1");
        check(nearly_equal(result[0].box[2], 0.08f), "geometry x2");
        check(nearly_equal(result[0].box[3], 0.08f), "geometry y2");
    }

    zhulong::ppe::PpeDecodeBuffers bad_buffers;
    const zhulong::ppe::Yolov8PpeGeometry bad{0.0f, 0, 0, 100, 100};
    zhulong::ppe::decode_yolov8_ppe({head.view}, bad, 0.5f, 0.45f, bad_buffers);
    check(bad_buffers.detections.empty(), "zero scale returns empty");
}

void test_decode_nms() {
    const zhulong::ppe::Yolov8PpeGeometry geometry{1.0f, 0, 0, 32, 32};
    zhulong::ppe::PpeDecodeBuffers buffers;

    HeadFixture first(2, 8);
    HeadFixture second(2, 8);
    first.set_anchor(1, 1, zhulong::ppe::kClassHeadNoHelmet, 0.9f);
    second.set_anchor(1, 1, zhulong::ppe::kClassHeadNoHelmet, 0.8f);
    zhulong::ppe::decode_yolov8_ppe({first.view, second.view}, geometry, 0.5f, 0.45f, buffers);
    check(buffers.detections.size() == 1, "same-class NMS suppresses duplicate");

    HeadFixture helmet_head(2, 8);
    helmet_head.set_anchor(1, 1, zhulong::ppe::kClassHeadHelmet, 0.7f);
    zhulong::ppe::decode_yolov8_ppe({first.view, helmet_head.view}, geometry, 0.5f, 0.45f, buffers);
    const auto& distinct = buffers.detections;
    check(distinct.size() == 2, "per-class NMS keeps both classes for conflict resolver");
    if (distinct.size() == 2) {
        check(distinct[0].class_id == zhulong::ppe::kClassHeadNoHelmet, "higher score kept first");
        check(distinct[1].class_id == zhulong::ppe::kClassHeadHelmet, "other class kept second");
    }
}

void test_head_filtering_and_conflict() {
    const auto no_helmet =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.85f, zhulong::ppe::kClassHeadNoHelmet);
    const auto helmet =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.80f, zhulong::ppe::kClassHeadHelmet);

    std::vector<zhulong::ppe::HelmetDecision> decisions;
    zhulong::ppe::associate_helmets({no_helmet}, 0.02f, decisions);
    check(decisions.size() == 1, "single violation head");
    if (decisions.size() == 1) {
        check(decisions[0].violation, "violation flag");
        check(nearly_equal(decisions[0].box[0], 0.25f), "uses head box");
        check(nearly_equal(decisions[0].score, 0.85f), "uses head score");
    }

    zhulong::ppe::associate_helmets({helmet}, 0.02f, decisions);
    check(decisions.size() == 1 && !decisions[0].violation, "single compliant head");

    const auto tiny =
        make_detection(0.20f, 0.20f, 0.22f, 0.21f, 0.90f, zhulong::ppe::kClassHeadNoHelmet);
    zhulong::ppe::associate_helmets({tiny}, 0.02f, decisions);
    check(decisions.empty(), "too-small head skipped");

    zhulong::ppe::associate_helmets({helmet, no_helmet}, 0.02f, decisions);
    check(decisions.size() == 1 && decisions[0].violation, "higher-score head wins conflict");

    const auto helmet_tie =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.85f, zhulong::ppe::kClassHeadHelmet);
    zhulong::ppe::associate_helmets({helmet_tie, no_helmet}, 0.02f, decisions);
    check(decisions.size() == 1 && decisions[0].violation, "tie prefers no_helmet");

    const auto helmet_slightly_higher =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.85f + 1e-5f, zhulong::ppe::kClassHeadHelmet);
    zhulong::ppe::associate_helmets({helmet_slightly_higher, no_helmet}, 0.02f, decisions);
    check(decisions.size() == 1 && decisions[0].violation, "epsilon-equal scores treat as tie");

    const auto helmet_clearly_higher =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.85f + 1e-3f, zhulong::ppe::kClassHeadHelmet);
    zhulong::ppe::associate_helmets({helmet_clearly_higher, no_helmet}, 0.02f, decisions);
    check(decisions.size() == 1 && !decisions[0].violation, "clear score gap beats tie rule");
}

void test_truncation() {
    std::vector<zhulong::Detection> detections;
    for (int i = 0; i < 200; ++i) {
        const float x = static_cast<float>(i % 20) * 0.04f + 0.001f;
        const float y = static_cast<float>(i / 20) * 0.08f + 0.001f;
        detections.push_back(
            make_detection(x, y, x + 0.03f, y + 0.05f, 0.85f, zhulong::ppe::kClassHeadNoHelmet));
    }

    std::vector<zhulong::ppe::HelmetDecision> decisions;
    zhulong::ppe::associate_helmets(detections, 0.02f, decisions);
    check(decisions.size() == zhulong::ppe::kPpeMaxDetections,
          "filtering truncates to kPpeMaxDetections");
}

zhulong::ppe::HelmetDecision make_decision(bool violation, float x1 = 0.2f, float y1 = 0.2f,
                                           float x2 = 0.3f, float y2 = 0.35f,
                                           float score = 0.85f) {
    zhulong::ppe::HelmetDecision decision;
    decision.box[0] = x1;
    decision.box[1] = y1;
    decision.box[2] = x2;
    decision.box[3] = y2;
    decision.score = score;
    decision.violation = violation;
    return decision;
}

void test_tracker() {
    const auto violation = make_decision(true);
    const auto compliant = make_decision(false);

    zhulong::ppe::ViolationTracker tracker;
    check(tracker.confirm(1, {violation}, 2).empty(), "first violation not confirmed");
    check(tracker.track_count(1) == 1, "first violation creates track");
    const auto confirmed = tracker.confirm(1, {violation}, 2);
    check(confirmed.size() == 1, "second violation confirmed");
    if (confirmed.size() == 1) {
        check(confirmed[0].label == "no_helmet", "confirmed label");
        check(confirmed[0].class_id == zhulong::ppe::kClassHeadNoHelmet, "confirmed class");
        check(nearly_equal(confirmed[0].confidence, 0.85f), "confirmed head score");
    }

    check(tracker.confirm(1, {compliant}, 2).empty(), "compliant resets streak");
    check(tracker.confirm(1, {violation}, 2).empty(), "violation after reset needs two frames");
    check(tracker.confirm(1, {violation}, 2).size() == 1, "re-confirmed after reset");

    zhulong::ppe::ViolationTracker immediate;
    check(immediate.confirm(5, {violation}, 0).size() == 1, "confirm_frames clamps to 1");

    zhulong::ppe::ViolationTracker capped;
    const int capped_required = zhulong::ppe::kMaxConfirmFrames;
    for (int frame = 1; frame < capped_required; ++frame) {
        check(capped.confirm(5, {violation}, 99).empty(), "confirm_frames clamps to kMax");
    }
    check(capped.confirm(5, {violation}, 99).size() == 1, "clamped upper bound confirms");

    zhulong::ppe::ViolationTracker isolated;
    isolated.confirm(1, {violation}, 2);
    isolated.confirm(2, {violation}, 2);
    check(isolated.confirm(1, {violation}, 2).size() == 1, "task 1 confirmed independently");
    check(isolated.confirm(2, {violation}, 2).size() == 1, "task 2 confirmed independently");

    zhulong::ppe::ViolationTracker expiry;
    expiry.confirm(7, {violation}, 2);
    for (int i = 0; i <= zhulong::ppe::ViolationTracker::kMaxMissedFrames; ++i) {
        expiry.confirm(7, {}, 2);
    }
    check(expiry.track_count(7) == 0, "stale track evicted");
    check(expiry.confirm(7, {violation}, 2).empty(), "evicted track starts over");
    check(expiry.confirm(7, {violation}, 2).size() == 1, "new track confirms after two frames");

    zhulong::ppe::ViolationTracker manual_reset;
    manual_reset.confirm(9, {violation}, 2);
    check(manual_reset.track_count(9) == 1, "track present before reset");
    manual_reset.reset(9);
    check(manual_reset.track_count(9) == 0, "reset clears task tracks");
    check(manual_reset.confirm(9, {violation}, 2).empty(), "reset restarts the streak");
}

/**
 * @brief 两个高度重叠的人头不得被压进同一条轨迹（回归：匹配分支漏置 used 标记）
 */
void test_tracker_overlap_regression() {
    const auto left = make_decision(true, 0.10f, 0.10f, 0.25f, 0.25f);
    const auto right = make_decision(true, 0.105f, 0.105f, 0.255f, 0.255f);

    zhulong::ppe::ViolationTracker tracker;
    check(tracker.confirm(7, {left}, 2).empty(), "overlap frame 1 not confirmed");
    check(tracker.track_count(7) == 1, "overlap frame 1 has one track");

    const auto confirmed = tracker.confirm(7, {left, right}, 2);
    check(confirmed.size() == 1, "overlap frame 2 confirms only the matched track");
    check(tracker.track_count(7) == 2, "overlap frame 2 opens a second track");
}

} // namespace

int main() {
    test_config();
    test_class_labels();
    test_decode_head();
    test_decode_buffers_reused();
    test_decode_geometry();
    test_decode_nms();
    test_head_filtering_and_conflict();
    test_truncation();
    test_tracker();
    test_tracker_overlap_regression();

    if (g_failures != 0) {
        std::cerr << g_failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all ppe_helmet unit tests passed\n";
    return 0;
}
