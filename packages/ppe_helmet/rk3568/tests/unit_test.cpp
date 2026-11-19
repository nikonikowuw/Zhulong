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
          box(static_cast<size_t>(64) * static_cast<size_t>(grid_size) *
                  static_cast<size_t>(grid_size),
              -20.0f),
          score(static_cast<size_t>(17) * static_cast<size_t>(grid_size) *
                    static_cast<size_t>(grid_size),
                -10.0f) {
        view.box = box.data();
        view.score = score.data();
        view.grid_h = grid_size;
        view.grid_w = grid_size;
        view.stride_h = stride;
        view.stride_w = stride;
    }

    /**
     * @brief 把 anchor 配成「左/上距离=0，右/下距离=1 bin」的确定性 DFL
     */
    void set_anchor(int row, int col, int32_t class_id, float logit = 10.0f) {
        const size_t plane = static_cast<size_t>(grid) * static_cast<size_t>(grid);
        const size_t anchor = static_cast<size_t>(row) * static_cast<size_t>(grid) +
                              static_cast<size_t>(col);
        score[static_cast<size_t>(class_id) * plane + anchor] = logit;
        box[(0 * 16 + 0) * plane + anchor] = 20.0f;
        box[(1 * 16 + 0) * plane + anchor] = 20.0f;
        box[(2 * 16 + 1) * plane + anchor] = 20.0f;
        box[(3 * 16 + 1) * plane + anchor] = 20.0f;
    }
};

void test_config() {
    const auto defaults = zhulong::ppe::resolve_ppe_config("", zhulong::ppe::PpeConfig{});
    check(defaults.score_threshold == 0.25f, "default score_threshold");
    check(defaults.nms_threshold == 0.45f, "default nms_threshold");
    check(defaults.min_person_height_ratio == 0.08f, "default min_person_height_ratio");
    check(!defaults.report_compliant, "default report_compliant");
    check(defaults.confirm_frames == 2, "default confirm_frames");

    const auto custom = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.5,"nms_threshold":0.3,"min_person_height_ratio":0.1,"report_compliant":true,"confirm_frames":3})",
        zhulong::ppe::PpeConfig{});
    check(custom.score_threshold == 0.5f, "custom score_threshold");
    check(custom.nms_threshold == 0.3f, "custom nms_threshold");
    check(custom.min_person_height_ratio == 0.1f, "custom min_person_height_ratio");
    check(custom.report_compliant, "custom report_compliant");
    check(custom.confirm_frames == 3, "custom confirm_frames");

    const auto out_of_range = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.99,"nms_threshold":0.01,"min_person_height_ratio":0.9,"confirm_frames":2.5})",
        zhulong::ppe::PpeConfig{});
    check(out_of_range.score_threshold == 0.25f, "out-of-range score falls back");
    check(out_of_range.nms_threshold == 0.45f, "out-of-range nms falls back");
    check(out_of_range.min_person_height_ratio == 0.08f, "out-of-range height falls back");
    check(out_of_range.confirm_frames == 2, "non-integer confirm_frames falls back");

    const auto nested = zhulong::ppe::resolve_ppe_config(
        R"({"nested":{"score_threshold":0.8},"note":"score_threshold: 0.9"})",
        zhulong::ppe::PpeConfig{});
    check(nested.score_threshold == 0.25f, "nested/string keys must not be read");

    const auto malformed = zhulong::ppe::resolve_ppe_config(
        R"({"score_threshold":0.4x})", zhulong::ppe::PpeConfig{});
    check(malformed.score_threshold == 0.25f, "trailing garbage must be rejected");
}

void test_decode_person() {
    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassPerson);
    const zhulong::ppe::Yolov8PpeGeometry geometry{1.0f, 0, 0, 32, 32};
    std::vector<zhulong::Detection> scratch;

    const auto result = zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, scratch);
    check(result.size() == 1, "decode returns one person");
    if (result.size() == 1) {
        check(result[0].class_id == zhulong::ppe::kClassPerson, "decode class person");
        check(result[0].label == "person", "decode label person");
        check(result[0].confidence > 0.99f, "decode sigmoid score");
        check(nearly_equal(result[0].box[0], 0.375f), "decode x1");
        check(nearly_equal(result[0].box[1], 0.375f), "decode y1");
        check(nearly_equal(result[0].box[2], 0.625f), "decode x2");
        check(nearly_equal(result[0].box[3], 0.625f), "decode y2");
    }
}

void test_decode_geometry() {
    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassPerson);
    const zhulong::ppe::Yolov8PpeGeometry geometry{2.0f, 0, 0, 100, 100};
    std::vector<zhulong::Detection> scratch;

    const auto result = zhulong::ppe::decode_yolov8_ppe({head.view}, geometry, 0.5f, 0.45f, scratch);
    check(result.size() == 1, "decode geometry returns one");
    if (result.size() == 1) {
        check(nearly_equal(result[0].box[0], 0.06f), "geometry x1");
        check(nearly_equal(result[0].box[1], 0.06f), "geometry y1");
        check(nearly_equal(result[0].box[2], 0.10f), "geometry x2");
        check(nearly_equal(result[0].box[3], 0.10f), "geometry y2");
    }

    std::vector<zhulong::Detection> scratch_bad;
    const zhulong::ppe::Yolov8PpeGeometry bad{0.0f, 0, 0, 100, 100};
    check(zhulong::ppe::decode_yolov8_ppe({head.view}, bad, 0.5f, 0.45f, scratch_bad).empty(),
          "zero scale returns empty");
}

void test_decode_nms() {
    const zhulong::ppe::Yolov8PpeGeometry geometry{1.0f, 0, 0, 32, 32};
    std::vector<zhulong::Detection> scratch;

    HeadFixture first(2, 8);
    HeadFixture second(2, 8);
    first.set_anchor(1, 1, zhulong::ppe::kClassPerson);
    second.set_anchor(1, 1, zhulong::ppe::kClassPerson);
    check(zhulong::ppe::decode_yolov8_ppe({first.view, second.view}, geometry, 0.5f, 0.45f, scratch)
                  .size() == 1,
          "same-class NMS suppresses duplicate");

    HeadFixture head(2, 8);
    head.set_anchor(1, 1, zhulong::ppe::kClassHeadNoHelmet);
    const auto distinct = zhulong::ppe::decode_yolov8_ppe(
        {first.view, head.view}, geometry, 0.5f, 0.45f, scratch);
    check(distinct.size() == 2, "per-class NMS keeps person and head");
    if (distinct.size() == 2) {
        check(distinct[0].class_id == zhulong::ppe::kClassPerson, "person kept first");
        check(distinct[1].class_id == zhulong::ppe::kClassHeadNoHelmet, "head kept second");
    }
}

void test_association() {
    const auto person = make_detection(0.2f, 0.2f, 0.4f, 0.8f, 0.9f, zhulong::ppe::kClassPerson);
    const auto no_helmet =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.8f, zhulong::ppe::kClassHeadNoHelmet);
    const auto helmet =
        make_detection(0.25f, 0.20f, 0.35f, 0.32f, 0.8f, zhulong::ppe::kClassHeadHelmet);

    const auto violation = zhulong::ppe::associate_helmets({person, no_helmet}, 0.08f);
    check(violation.size() == 1, "association violation count");
    if (violation.size() == 1) {
        check(violation[0].violation, "association violation flag");
        check(nearly_equal(violation[0].box[0], 0.2f), "association uses person box");
        check(nearly_equal(violation[0].person_score, 0.9f), "association person score");
        check(nearly_equal(violation[0].head_score, 0.8f), "association head score");
    }

    const auto compliant = zhulong::ppe::associate_helmets({person, helmet}, 0.08f);
    check(compliant.size() == 1 && !compliant[0].violation, "association compliant");

    check(zhulong::ppe::associate_helmets({person}, 0.08f).empty(), "no head means unknown");
    check(zhulong::ppe::associate_helmets({no_helmet}, 0.08f).empty(), "no person means unknown");

    const auto tiny = make_detection(0.2f, 0.2f, 0.4f, 0.25f, 0.9f, zhulong::ppe::kClassPerson);
    check(zhulong::ppe::associate_helmets({tiny, no_helmet}, 0.08f).empty(),
          "too-small person skipped");

    const auto large_head =
        make_detection(0.22f, 0.18f, 0.38f, 0.50f, 0.8f, zhulong::ppe::kClassHeadNoHelmet);
    check(zhulong::ppe::associate_helmets({person, large_head}, 0.08f).empty(),
          "oversized head rejected");

    const auto left_person =
        make_detection(0.1f, 0.2f, 0.3f, 0.8f, 0.9f, zhulong::ppe::kClassPerson);
    const auto right_person =
        make_detection(0.25f, 0.2f, 0.45f, 0.8f, 0.9f, zhulong::ppe::kClassPerson);
    const auto shared_head =
        make_detection(0.26f, 0.2f, 0.34f, 0.32f, 0.8f, zhulong::ppe::kClassHeadNoHelmet);
    const auto assigned =
        zhulong::ppe::associate_helmets({left_person, right_person, shared_head}, 0.08f);
    check(assigned.size() == 1, "shared head assigned once");
    if (assigned.size() == 1) {
        check(nearly_equal(assigned[0].box[0], 0.25f), "shared head goes to best containment");
    }

    const auto helmet_low =
        make_detection(0.25f, 0.2f, 0.35f, 0.32f, 0.6f, zhulong::ppe::kClassHeadHelmet);
    const auto no_helmet_high =
        make_detection(0.25f, 0.2f, 0.35f, 0.32f, 0.9f, zhulong::ppe::kClassHeadNoHelmet);
    const auto both =
        zhulong::ppe::associate_helmets({person, helmet_low, no_helmet_high}, 0.08f);
    check(both.size() == 1 && both[0].violation, "higher-score head wins");

    const auto helmet_tie =
        make_detection(0.25f, 0.2f, 0.35f, 0.32f, 0.9f, zhulong::ppe::kClassHeadHelmet);
    const auto tie = zhulong::ppe::associate_helmets({person, helmet_tie, no_helmet_high}, 0.08f);
    check(tie.size() == 1 && tie[0].violation, "tie prefers no_helmet");
}

zhulong::ppe::HelmetDecision make_decision(bool violation, float x1 = 0.2f, float y1 = 0.2f,
                                           float x2 = 0.4f, float y2 = 0.8f,
                                           float person_score = 0.9f,
                                           float head_score = 0.8f) {
    zhulong::ppe::HelmetDecision decision;
    decision.box[0] = x1;
    decision.box[1] = y1;
    decision.box[2] = x2;
    decision.box[3] = y2;
    decision.person_score = person_score;
    decision.head_score = head_score;
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
        check(nearly_equal(confirmed[0].confidence, 0.8f), "confirmed min score");
    }

    check(tracker.confirm(1, {compliant}, 2).empty(), "compliant resets streak");
    check(tracker.confirm(1, {violation}, 2).empty(), "violation after reset needs two frames");
    check(tracker.confirm(1, {violation}, 2).size() == 1, "re-confirmed after reset");

    zhulong::ppe::ViolationTracker immediate;
    check(immediate.confirm(5, {violation}, 0).size() == 1, "confirm_frames clamps to 1");

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
}

} // namespace

int main() {
    test_config();
    test_decode_person();
    test_decode_geometry();
    test_decode_nms();
    test_association();
    test_tracker();

    if (g_failures != 0) {
        std::cerr << g_failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all ppe_helmet unit tests passed\n";
    return 0;
}
