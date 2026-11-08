/**
 * @file motion_detector_test.cpp
 * @brief MotionDetector 单元测试（纯合成 NV12 帧，不需要解码器与真实摄像机）
 *
 * ⚠️ 本文件全靠 assert()。构建侧必须把目标加进 CMake 的 ASSERT_TEST_TARGETS
 *    （显式 -UNDEBUG），否则 RelWithDebInfo 下断言被编译掉，测试只是「假绿」。
 */

#include "pipeline/motion_detector.hpp"

#include <cassert>
#include <cstdint>
#include <iostream>
#include <memory>
#include <vector>

using namespace zhulong;

namespace {

/** @brief 合成一帧纯色 NV12，返回持有像素内存的帧对象 */
HardwareFrame create_dummy_nv12(uint32_t width, uint32_t height, uint8_t y_fill = 50) {
    HardwareFrame frame{};
    frame.width = width;
    frame.height = height;
    frame.stride = width;
    frame.vstride = height;
    frame.format = FRAME_FORMAT_NV12;

    const size_t y_size = static_cast<size_t>(width) * height;
    const size_t uv_size = static_cast<size_t>(width) * (height / 2);
    auto buffer = std::make_shared<std::vector<uint8_t>>(y_size + uv_size, 128);
    std::fill(buffer->begin(), buffer->begin() + static_cast<std::ptrdiff_t>(y_size), y_fill);

    frame.host_planes[0] = buffer->data();
    frame.host_planes[1] = buffer->data() + y_size;
    frame.plane_strides[0] = width;
    frame.plane_strides[1] = width;

    frame.release_fn = [buffer](HardwareFrame*) {
        // 帧存活期间由本闭包持有像素内存
    };

    return frame;
}

/** @brief 在 Y 平面写一块亮矩形 */
void paint_y_rect(HardwareFrame& frame, uint32_t x0, uint32_t y0, uint32_t x1, uint32_t y1,
                  uint8_t value) {
    uint8_t* y_plane = frame.host_planes[0];
    for (uint32_t y = y0; y < y1; ++y) {
        for (uint32_t x = x0; x < x1; ++x) {
            y_plane[static_cast<size_t>(y) * frame.stride + x] = value;
        }
    }
}

void test_initial_and_still_frames() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);

    MotionResult res1{};
    assert(detector.detect(frame1, res1));
    // 首帧无参考底图，按 fail-open 放行（无外接框）：流刚起来时画面里可能已经站着人，
    // 判静会一直停到心跳（MOTION_HEARTBEAT_FRAMES）才恢复推理。
    assert(res1.has_motion);
    assert(res1.box_count == 0);

    auto frame2 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    assert(!res2.has_motion);
    assert(res2.box_count == 0);

    std::cout << "[PASS] test_initial_and_still_frames" << std::endl;
}

void test_local_motion_detection() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res1{};
    assert(detector.detect(frame1, res1));

    // 右下角 (x: 1200~1500, y: 600~900) 写入明显亮块
    auto frame2 = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(frame2, 1200, 600, 1500, 900, 220);

    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    assert(res2.has_motion);
    assert(res2.box_count == 1);

    const auto& box = res2.boxes[0];
    assert(box.x1 >= 0.5f && box.x2 <= 1.0f);
    assert(box.y1 >= 0.4f && box.y2 <= 1.0f);
    assert(box.x2 > box.x1);
    assert(box.y2 > box.y1);

    std::cout << "[PASS] test_local_motion_detection" << std::endl;
}

// PRD R5：多框聚类——两块互不相邻的运动区域必须产出两个独立外接框
void test_multi_box_clustering() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res1{};
    assert(detector.detect(frame1, res1));

    auto frame2 = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(frame2, 120, 120, 360, 360, 220);   // 左上
    paint_y_rect(frame2, 1500, 660, 1800, 900, 220); // 右下

    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    assert(res2.has_motion);
    assert(res2.box_count == 2);

    // 结果按面积降序；两块面积相同，故按位置断言「一左上、一右下」而不依赖顺序
    bool has_top_left = false;
    bool has_bottom_right = false;
    for (uint32_t i = 0; i < res2.box_count; ++i) {
        const auto& box = res2.boxes[i];
        assert(box.x2 > box.x1);
        assert(box.y2 > box.y1);
        if (box.x1 < 0.3f && box.y1 < 0.3f) {
            has_top_left = true;
        }
        if (box.x2 > 0.7f && box.y2 > 0.7f) {
            has_bottom_right = true;
        }
    }
    assert(has_top_left);
    assert(has_bottom_right);

    std::cout << "[PASS] test_multi_box_clustering" << std::endl;
}

void test_anti_flash_scene_change() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res1{};
    assert(detector.detect(frame1, res1));

    // 全屏光照跳变：整幅画面由 50 变成 200（超过 MAX_ACTIVE_RATIO）
    auto frame2 = create_dummy_nv12(1920, 1080, 200);
    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    // 光照跳变被抑制：判为无有效运动，且不产生运动外接框（此路径 hold-off 尚为 0，
    // 因为跳变前从未检测到运动）
    assert(!res2.has_motion);
    assert(res2.box_count == 0);

    std::cout << "[PASS] test_anti_flash_scene_change" << std::endl;
}

void test_micro_noise_filtered() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res1{};
    assert(detector.detect(frame1, res1));

    // 仅修改极少数离散像素 (< MIN_ACTIVE_PIXELS)
    auto frame2 = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(frame2, 100, 100, 110, 101, 250);

    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    assert(!res2.has_motion);
    assert(res2.box_count == 0);

    std::cout << "[PASS] test_micro_noise_filtered" << std::endl;
}

// hold-off：运动消失后必须继续放行 motion_hold_frames 帧，避免「站着不动 = 目标消失」
void test_motion_hold_off() {
    MotionDetector detector;
    auto still = create_dummy_nv12(1920, 1080, 50);
    MotionResult res{};
    assert(detector.detect(still, res));

    auto moving = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(moving, 600, 400, 800, 600, 220);
    assert(detector.detect(moving, res));
    assert(res.has_motion);

    // 运动结束：静止帧在保持期内仍然放行（无外接框），之后才真正判静
    for (uint32_t i = 0; i < MotionDetector::motion_hold_frames; ++i) {
        auto after = create_dummy_nv12(1920, 1080, 50);
        paint_y_rect(after, 600, 400, 800, 600, 220);
        assert(detector.detect(after, res));
        assert(res.has_motion);
        assert(res.box_count == 0);
    }

    auto settled = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(settled, 600, 400, 800, 600, 220);
    assert(detector.detect(settled, res));
    assert(!res.has_motion);
    assert(res.box_count == 0);

    std::cout << "[PASS] test_motion_hold_off" << std::endl;
}

void test_reset() {
    MotionDetector detector;
    auto frame1 = create_dummy_nv12(1920, 1080, 50);
    MotionResult res1{};
    assert(detector.detect(frame1, res1));

    detector.reset();

    // reset 后再次输入任何帧，都按首帧处理（只建底图，fail-open 放行）
    auto frame2 = create_dummy_nv12(1920, 1080, 200);
    MotionResult res2{};
    assert(detector.detect(frame2, res2));
    assert(res2.has_motion);
    assert(res2.box_count == 0);

    std::cout << "[PASS] test_reset" << std::endl;
}

// 画面长时间静止时的心跳保底巡检 (Heartbeat Keep-Alive)
void test_still_heartbeat_keepalive() {
    MotionDetector detector;
    auto base_frame = create_dummy_nv12(1920, 1080, 50);

    MotionResult res{};
    // 首帧：建立底图，按 fail-open 放行（同时作为心跳计数的 0 点）
    assert(detector.detect(base_frame, res));
    assert(res.has_motion);
    assert(res.box_count == 0);

    // 触发一次有效运动
    auto moving = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(moving, 600, 400, 800, 600, 220);
    assert(detector.detect(moving, res));
    assert(res.has_motion);

    // 画面停止运动，进入保持期 (motion_hold_frames = 10)
    for (uint32_t i = 0; i < MotionDetector::motion_hold_frames; ++i) {
        auto after = create_dummy_nv12(1920, 1080, 50);
        paint_y_rect(after, 600, 400, 800, 600, 220);
        assert(detector.detect(after, res));
        assert(res.has_motion);
        assert(res.box_count == 0);
    }

    // 正式进入纯静止期：持续送入静止帧
    // 第 1 ~ 49 帧静止：必须维持 has_motion == false
    for (uint32_t i = 1; i < MotionDetector::motion_heartbeat_frames; ++i) {
        auto still = create_dummy_nv12(1920, 1080, 50);
        paint_y_rect(still, 600, 400, 800, 600, 220);
        assert(detector.detect(still, res));
        assert(!res.has_motion);
        assert(res.box_count == 0);
    }

    // 第 50 帧：达到心跳阈值，强制瞬态放行 (has_motion = true, box_count = 0)
    auto hb_frame = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(hb_frame, 600, 400, 800, 600, 220);
    assert(detector.detect(hb_frame, res));
    assert(res.has_motion);
    assert(res.box_count == 0);

    // ⚠️ 核心断言：第 51 帧必须立即跌回静止（has_motion == false），绝不能连带触发 10 帧 hold-off！
    auto next_frame = create_dummy_nv12(1920, 1080, 50);
    paint_y_rect(next_frame, 600, 400, 800, 600, 220);
    assert(detector.detect(next_frame, res));
    assert(!res.has_motion);
    assert(res.box_count == 0);

    // 验证第二次心跳：再送 48 帧静止（累积 1 + 48 = 49 帧静止），仍为 false
    for (uint32_t i = 0; i < MotionDetector::motion_heartbeat_frames - 2; ++i) {
        auto still = create_dummy_nv12(1920, 1080, 50);
        paint_y_rect(still, 600, 400, 800, 600, 220);
        assert(detector.detect(still, res));
        assert(!res.has_motion);
    }

    // 第 100 帧（第二次满 50 帧静止）：再次触发心跳
    assert(detector.detect(hb_frame, res));
    assert(res.has_motion);
    assert(res.box_count == 0);

    std::cout << "[PASS] test_still_heartbeat_keepalive" << std::endl;
}

// 非法输入必须返回 false，由调用方降级放行（fail-open）
void test_unsupported_input_fails_open() {
    MotionDetector detector;

    HardwareFrame unknown{};
    unknown.width = 1920;
    unknown.height = 1080;
    unknown.stride = 1920;
    unknown.format = FRAME_FORMAT_UNKNOWN;
    MotionResult res{};
    assert(!detector.detect(unknown, res));
    assert(res.has_motion); // 默认放行，绝不因为没检测成功就跳过推理

    HardwareFrame no_pixels{};
    no_pixels.width = 1920;
    no_pixels.height = 1080;
    no_pixels.stride = 1920;
    no_pixels.format = FRAME_FORMAT_NV12;
    assert(!detector.detect(no_pixels, res));
    assert(res.has_motion);

    std::cout << "[PASS] test_unsupported_input_fails_open" << std::endl;
}

void test_plane_stride_padded() {
    MotionDetector detector;
    uint32_t width = 1920;
    uint32_t height = 1080;
    uint32_t pitch = 2048;
    HardwareFrame frame1{};
    frame1.width = width;
    frame1.height = height;
    frame1.stride = width;
    frame1.plane_strides[0] = pitch;
    frame1.format = FRAME_FORMAT_NV12;
    std::vector<uint8_t> buf1(pitch * height, 50);
    frame1.host_planes[0] = buf1.data();
    MotionResult res1{};
    assert(detector.detect(frame1, res1));
    assert(res1.has_motion);   // 首帧 fail-open：没有参考底图时一律放行
    assert(res1.box_count == 0);

    std::cout << "[PASS] test_plane_stride_padded" << std::endl;
}

} // namespace

int main() {
    std::cout << "Running MotionDetector unit tests..." << std::endl;
    test_initial_and_still_frames();
    test_local_motion_detection();
    test_multi_box_clustering();
    test_anti_flash_scene_change();
    test_micro_noise_filtered();
    test_motion_hold_off();
    test_reset();
    test_still_heartbeat_keepalive();
    test_unsupported_input_fails_open();
    test_plane_stride_padded();
    std::cout << "All MotionDetector unit tests PASSED!" << std::endl;
    return 0;
}
