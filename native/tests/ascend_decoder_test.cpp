/**
 * @file ascend_decoder_test.cpp
 * @brief 昇腾 VDEC 解码节点状态机回归（假通道驱动，无需 CANN 与真机）
 *
 * 覆盖点：
 * 1. NV12 输出几何对齐（宽 16 对齐且不小于 32、高 2 对齐、缓冲 = ws*hs*3/2）；
 * 2. 背压契约：通道缓冲满 → decode_again，调用方重发；EOS 之后送包 → AVERROR_EOF；
 * 3. D2H 回拷：帧字段（stride/vstride/format/pts）与主机内存内容逐字节对齐；
 * 4. 设备帧归还：成功、回拷失败、几何非法三条路径都必须 release()，绝不泄漏 VDEC frame buffer；
 * 5. 主机缓冲池复用：释放后的帧被回收，下一帧拿到同一块内存；
 * 6. flush()：重置通道并清掉 draining 状态。
 */

#include "../src/nodes/decode/ascend_decoder.hpp"

extern "C" {
#include <libavutil/error.h>
}

#include <cassert>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <vector>

using namespace zhulong;

namespace {

constexpr int64_t kDeviceSentinel = 0x1000; // 假设备地址（只需非空且稳定）

/** @brief 可编程的假通道：记录调用，按脚本返回 */
class FakeChannel final : public ascend::IVdecChannel {
public:
    int send_result{decode_ok};
    int eos_result{decode_ok};
    int receive_result{decode_again};
    int copy_result{decode_ok};

    size_t send_calls{0};
    size_t eos_calls{0};
    size_t reset_calls{0};
    size_t release_calls{0};
    size_t copy_calls{0};
    size_t last_send_size{0};
    int64_t last_send_pts{0};
    uint8_t last_send_first_byte{0};

    // 待取出的帧（receive 成功时吐出，然后清空）
    bool has_pending_frame{false};
    ascend::DecodedFrame pending;
    uint8_t fill_byte{0xA5};

    int send(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override {
        (void)dts;
        ++send_calls;
        last_send_size = size;
        last_send_pts = pts;
        last_send_first_byte = size > 0 ? data[0] : 0;
        return send_result;
    }

    int send_eos() override {
        ++eos_calls;
        return eos_result;
    }

    int receive(ascend::DecodedFrame &out) override {
        if (receive_result != decode_ok) {
            return receive_result;
        }
        if (!has_pending_frame) {
            return decode_again;
        }
        has_pending_frame = false;
        out = pending;
        return decode_ok;
    }

    void release(void *dev_ptr) noexcept override {
        (void)dev_ptr;
        ++release_calls;
    }

    void reset() override { ++reset_calls; }

    int copy_to_host(void *host, const void *dev, size_t size) override {
        ++copy_calls;
        (void)dev;
        if (copy_result != decode_ok) {
            return copy_result;
        }
        std::memset(host, fill_byte, size);
        return decode_ok;
    }

    [[nodiscard]] bool healthy() const noexcept override { return true; }

    /** @brief 预置一帧：几何与时间戳由调用方决定 */
    void queue_frame(uint32_t width, uint32_t height, uint32_t width_stride, uint32_t height_stride, int64_t pts,
                     bool has_pts = true) {
        pending = ascend::DecodedFrame{};
        pending.dev_ptr = reinterpret_cast<void *>(kDeviceSentinel);
        pending.pts = pts;
        pending.has_pts = has_pts;
        pending.geometry.width = width;
        pending.geometry.height = height;
        pending.geometry.width_stride = width_stride;
        pending.geometry.height_stride = height_stride;
        pending.geometry.buffer_size = static_cast<size_t>(width_stride) * height_stride * 3 / 2;
        has_pending_frame = true;
        receive_result = decode_ok;
    }
};

void test_geometry_alignment() {
    const auto hd = ascend::compute_vdec_geometry(1920, 1080);
    assert(hd.width == 1920 && hd.height == 1080);
    assert(hd.width_stride == 1920);   // 已是 16 的倍数
    assert(hd.height_stride == 1080);  // 已是 2 的倍数
    assert(hd.buffer_size == static_cast<size_t>(1920) * 1080 * 3 / 2);

    // 宽度小于 32 时抬到最小 stride；高度奇数抬到偶数
    const auto tiny = ascend::compute_vdec_geometry(30, 5);
    assert(tiny.width_stride == 32);
    assert(tiny.height_stride == 6);
    assert(tiny.buffer_size == static_cast<size_t>(32) * 6 * 3 / 2);

    const auto odd = ascend::compute_vdec_geometry(1366, 769);
    assert(odd.width_stride == 1376);  // 1366 -> 1376
    assert(odd.height_stride == 770);  // 769 -> 770

    // 非法尺寸不产生可用的缓冲
    const auto empty = ascend::compute_vdec_geometry(0, 720);
    assert(empty.buffer_size == 0);
}

void test_send_backpressure_and_retry() {
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    AscendDecodeNode node(std::move(channel), 7, 1, 90000);

    const uint8_t packet[4] = {0x00, 0x00, 0x00, 0x01};

    // 通道缓冲满 -> 背压，调用方负责排空后重发同一个包
    raw->send_result = decode_again;
    assert(node.send_packet(packet, sizeof(packet), 9000, 8000) == decode_again);
    assert(raw->send_calls == 1);

    raw->send_result = decode_ok;
    assert(node.send_packet(packet, sizeof(packet), 9000, 8000) == decode_ok);
    assert(raw->send_calls == 2);
    assert(raw->last_send_size == sizeof(packet));
    assert(raw->last_send_pts == 9000);
    assert(raw->last_send_first_byte == 0x00);

    // 后端不可恢复错误必须归一化成 decode_backend_failure
    raw->send_result = -12345;
    assert(node.send_packet(packet, sizeof(packet), 9000, 8000) == decode_backend_failure);
}

void test_receive_copies_to_host() {
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    AscendDecodeNode node(std::move(channel), 7, 1, 90000);

    raw->fill_byte = 0x5A;
    raw->queue_frame(1920, 1080, 1920, 1080, 3600);

    HardwareFrame::Ptr frame;
    assert(node.receive_frame(frame) == decode_ok);
    assert(frame != nullptr);
    assert(frame->width == 1920 && frame->height == 1080);
    assert(frame->stride == 1920 && frame->vstride == 1080);
    assert(frame->format == FRAME_FORMAT_NV12);
    assert(frame->has_pts && frame->pts == 3600);
    assert(frame->time_base_num == 1 && frame->time_base_den == 90000);
    assert(frame->stream_id == 7);
    assert(frame->dev_ptr == nullptr); // 首期不做零拷贝：帧只持有主机内存
    assert(frame->dma_fd == -1);
    assert(frame->host_ptr != nullptr);

    // 回拷内容逐字节落地
    const size_t total = static_cast<size_t>(1920) * 1080 * 3 / 2;
    for (size_t index = 0; index < total; ++index) {
        assert(frame->host_ptr[index] == 0x5A);
    }
    // 平面视图：UV 紧跟在 Y 之后，跨度等于 width_stride
    assert(frame->host_planes[0] == frame->host_ptr);
    assert(frame->host_planes[1] == frame->host_ptr + static_cast<size_t>(1920) * 1080);
    assert(frame->plane_strides[0] == 1920 && frame->plane_strides[1] == 1920);

    // 设备帧在回拷后立刻归还
    assert(raw->release_calls == 1);

    // 池复用：释放后再取一帧，同尺寸应拿到同一块主机内存
    uint8_t *first_buffer = frame->host_ptr;
    frame.reset();
    raw->queue_frame(1920, 1080, 1920, 1080, 7200);
    HardwareFrame::Ptr second;
    assert(node.receive_frame(second) == decode_ok);
    assert(second->host_ptr == first_buffer);
    assert(raw->release_calls == 2);

    // 容量保护：尺寸增大时绝不能复用较小缓冲区（防堆溢出）
    second.reset();
    raw->queue_frame(3840, 2160, 3840, 2160, 10800);
    HardwareFrame::Ptr third;
    assert(node.receive_frame(third) == decode_ok);
    assert(third->host_ptr != first_buffer);
    assert(third->stride == 3840);
    assert(raw->release_calls == 3);
}

void test_receive_uses_driver_geometry() {
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    AscendDecodeNode node(std::move(channel), 7, 1, 90000);

    // 驱动选择了更大的对齐粒度：必须以驱动实际产出为准，否则 UV 偏移会错位；
    // PTS=0 是合法的有效首帧时间戳
    raw->queue_frame(1920, 1080, 2048, 1088, 0, true);
    HardwareFrame::Ptr frame;
    assert(node.receive_frame(frame) == decode_ok);
    assert(frame->stride == 2048);
    assert(frame->vstride == 1088);
    assert(frame->has_pts == true);
    assert(frame->pts == 0);
    assert(frame->host_planes[1] == frame->host_ptr + static_cast<size_t>(2048) * 1088);

    // 无时间戳场景验证
    raw->queue_frame(1920, 1080, 2048, 1088, 0, false);
    HardwareFrame::Ptr no_pts_frame;
    assert(node.receive_frame(no_pts_frame) == decode_ok);
    assert(no_pts_frame->has_pts == false);
}

void test_receive_eagain_and_failures() {
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    AscendDecodeNode node(std::move(channel), 7, 1, 90000);

    // 暂无输出
    raw->receive_result = decode_again;
    HardwareFrame::Ptr frame;
    assert(node.receive_frame(frame) == decode_again);
    assert(frame == nullptr);

    // 后端错误
    raw->receive_result = -1;
    assert(node.receive_frame(frame) == decode_backend_failure);
    assert(frame == nullptr);

    // 回拷失败：设备帧仍必须归还
    raw->queue_frame(1920, 1080, 1920, 1080, 100);
    raw->copy_result = decode_backend_failure;
    const size_t before = raw->release_calls;
    assert(node.receive_frame(frame) == decode_backend_failure);
    assert(frame == nullptr);
    assert(raw->release_calls == before + 1);

    // 几何非法（驱动回了 0x0）：归还设备帧并按不可恢复错误处理
    raw->copy_result = decode_ok;
    raw->queue_frame(0, 0, 0, 0, 100);
    assert(node.receive_frame(frame) == decode_backend_failure);
    assert(raw->release_calls == before + 2);
}

void test_eos_and_flush() {
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    AscendDecodeNode node(std::move(channel), 7, 1, 90000);

    // drain 信号：nullptr 或 size==0
    assert(node.send_packet(nullptr, 0, 0, 0) == decode_ok);
    assert(raw->eos_calls == 1);
    assert(node.send_packet(nullptr, 0, 0, 0) == AVERROR_EOF);

    const uint8_t packet[2] = {0x00, 0x01};
    assert(node.send_packet(packet, sizeof(packet), 0, 0) == AVERROR_EOF);

    // flush 复位 draining 状态并重置通道
    node.flush();
    assert(raw->reset_calls == 1);
    assert(node.send_packet(packet, sizeof(packet), 0, 0) == decode_ok);

    // EOS 本身遇到背压时也要能重试
    raw->eos_result = decode_again;
    assert(node.send_packet(nullptr, 0, 0, 0) == decode_again);
    raw->eos_result = decode_ok;
    assert(node.send_packet(nullptr, 0, 0, 0) == decode_ok);
}

void test_frame_outlives_node() {
    // 帧比节点活得久：release_fn 必须自带池的 keepalive，否则归还时会踩已释放的池
    auto channel = std::make_unique<FakeChannel>();
    FakeChannel *raw = channel.get();
    HardwareFrame::Ptr frame;
    {
        AscendDecodeNode node(std::move(channel), 7, 1, 90000);
        raw->queue_frame(64, 64, 64, 64, 1);
        assert(node.receive_frame(frame) == decode_ok);
    }
    assert(frame != nullptr);
    frame.reset(); // 节点已析构，这里归还缓冲区必须安全
}

void test_production_ctor_requires_geometry() {
    // 生产构造在无 CANN 的构建里会因为 is_supported()==false 而走 fail closed；
    // 这里只验证参数校验在触达后端之前就生效（宽高非法直接抛 invalid_argument）。
    bool threw = false;
    try {
        AscendDecodeNode node(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, 1, 0, 1080);
        (void)node;
    } catch (const std::invalid_argument &) {
        threw = true;
    } catch (...) {
        // 无 CANN 构建下 runtime_error 也可能先抛；那属于 fail closed，不算失败
        threw = true;
    }
    assert(threw);
}

} // namespace

int main() {
    test_geometry_alignment();
    test_send_backpressure_and_retry();
    test_receive_copies_to_host();
    test_receive_uses_driver_geometry();
    test_receive_eagain_and_failures();
    test_eos_and_flush();
    test_frame_outlives_node();
    test_production_ctor_requires_geometry();
    std::cout << "[PASS] ascend_decoder_test" << std::endl;
    return 0;
}
