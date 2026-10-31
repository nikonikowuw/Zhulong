/**
 * @file motion_gate_test.cpp
 * @brief Runner 骨架帧运动门禁的单元测试（报文解码 + 短路判定）
 *
 * 覆盖两条安全边界：
 * 1. 遗留 96 字节报文必须退化为「全帧推理」，不得因为读不到运动字段就既不推理也不 ACK；
 * 2. 只有显式 has_motion == 0 才短路，未知/未来取值一律放行（漏检代价高于算力）。
 *
 * ⚠️ 全靠 assert()：目标必须在 CMake 的 ASSERT_TEST_TARGETS 里（显式 -UNDEBUG）。
 */

#include "internal/motion_gate.hpp"

#include <cassert>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <vector>

using namespace zhulong;
using namespace zhulong::sdk;

namespace {

ipc::FrameDispatchMsg make_dispatch(uint32_t has_motion, uint32_t box_count) {
    ipc::FrameDispatchMsg msg{};
    msg.stream_id = 42;
    msg.frame_id = 7;
    msg.generation = 3;
    msg.slot_index = 1;
    msg.width = 1920;
    msg.height = 1080;
    msg.has_motion = has_motion;
    msg.motion_box_count = box_count;
    if (box_count > 0) {
        msg.motion_boxes[0] = ipc::IpcMotionBox{0.1f, 0.2f, 0.3f, 0.4f};
    }
    return msg;
}

// 当前线格式：运动字段必须完整解析
void test_current_format_roundtrip() {
    const auto sent = make_dispatch(0, 1);
    std::vector<uint8_t> payload(sizeof(sent));
    std::memcpy(payload.data(), &sent, sizeof(sent));

    ipc::FrameDispatchMsg decoded{};
    assert(gate::decode_frame_dispatch(payload.data(), payload.size(), decoded));

    assert(decoded.stream_id == sent.stream_id);
    assert(decoded.frame_id == sent.frame_id);
    assert(decoded.generation == sent.generation);
    assert(decoded.slot_index == sent.slot_index);
    assert(decoded.has_motion == 0);
    assert(decoded.motion_box_count == 1);
    assert(decoded.motion_boxes[0].x1 == 0.1f);
    assert(decoded.motion_boxes[0].y2 == 0.4f);

    std::cout << "[PASS] test_current_format_roundtrip" << std::endl;
}

// 遗留 96 字节报文：前 96 字节必须照常解析，运动字段必须放行
void test_legacy_dispatch_fails_open() {
    const auto sent = make_dispatch(0, 0);
    std::vector<uint8_t> legacy(ipc::LEGACY_FRAME_DISPATCH_SIZE);
    std::memcpy(legacy.data(), &sent, legacy.size());

    ipc::FrameDispatchMsg decoded{};
    assert(gate::decode_frame_dispatch(legacy.data(), legacy.size(), decoded));

    // 帧身份字段仍然是旧报文里的真值（ACK 靠它们归还租约）
    assert(decoded.stream_id == sent.stream_id);
    assert(decoded.frame_id == sent.frame_id);
    assert(decoded.generation == sent.generation);
    assert(decoded.slot_index == sent.slot_index);
    assert(decoded.width == sent.width);

    // 运动字段必须退化为「全帧推理」
    assert(decoded.has_motion == 1);
    assert(decoded.motion_box_count == 0);
    assert(!gate::should_skip_inference(decoded.has_motion));

    std::cout << "[PASS] test_legacy_dispatch_fails_open" << std::endl;
}

// 连遗留前缀都不够：必须拒绝，调用方按畸形报文丢弃
void test_truncated_dispatch_rejected() {
    ipc::FrameDispatchMsg decoded{};
    const std::vector<uint8_t> tiny(ipc::LEGACY_FRAME_DISPATCH_SIZE - 1, 0);
    assert(!gate::decode_frame_dispatch(tiny.data(), tiny.size(), decoded));

    // 空载荷 / 空指针同样拒绝
    assert(!gate::decode_frame_dispatch(nullptr, 0, decoded));
    const std::vector<uint8_t> empty;
    assert(!gate::decode_frame_dispatch(empty.data(), empty.size(), decoded));

    // 拒绝时输出必须是安全的默认值，不能带出未初始化内容
    assert(decoded.has_motion == 1);
    assert(decoded.motion_box_count == 0);

    std::cout << "[PASS] test_truncated_dispatch_rejected" << std::endl;
}

// 只有显式 0 才短路；1 与任何未知取值都放行
void test_skip_decision_is_strict() {
    assert(gate::should_skip_inference(0));
    assert(!gate::should_skip_inference(1));
    assert(!gate::should_skip_inference(2));
    assert(!gate::should_skip_inference(0xFFFFFFFFu));

    std::cout << "[PASS] test_skip_decision_is_strict" << std::endl;
}

// 禁用门禁时（如违停、在岗等时序算法），即使 has_motion == 0 也绝对不短路
void test_disable_motion_gate_always_runs() {
    // 门禁启用：0 必定短路，非 0 放行
    assert(gate::should_skip_inference(0, true));
    assert(!gate::should_skip_inference(1, true));

    // 门禁禁用：无论 has_motion 为何值，一律放行（should_skip_inference == false）
    assert(!gate::should_skip_inference(0, false));
    assert(!gate::should_skip_inference(1, false));
    assert(!gate::should_skip_inference(2, false));

    std::cout << "[PASS] test_disable_motion_gate_always_runs" << std::endl;
}

} // namespace

int main() {
    std::cout << "Running Runner motion gate unit tests..." << std::endl;
    test_current_format_roundtrip();
    test_legacy_dispatch_fails_open();
    test_truncated_dispatch_rejected();
    test_skip_decision_is_strict();
    test_disable_motion_gate_always_runs();
    std::cout << "All Runner motion gate unit tests PASSED!" << std::endl;
    return 0;
}
