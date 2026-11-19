/**
 * @file ascend_decoder.hpp
 * @brief 华为昇腾 DVPP VDEC 硬件解码节点
 *
 * 首期范围：解码后立即 D2H 回拷到主机内存，复用现有 CPU 前处理链路
 * （`nodes/preprocess/window_crop.cpp` 只认 `host_ptr`），暂不做零拷贝。
 *
 * 与 MPP 后端的差异（详见 .trellis/spec/native/hardware-backends.md §8）：
 * - `hi_mpi_vdec_*` 是拉取式接口：没有回调线程，帧由 `receive()` 主动取出；
 * - 设备帧归通道所有，`receive()` 借出后必须归还，因此节点在回拷完成后立刻 `release()`，
 *   主机内存才是帧的真正载体；
 * - 通道创建必须已知编码宽高，故 `IDecodeNode::create()` 增加了 width/height 参数。
 */

#pragma once

#include "nodes/decode/ascend_vdec.hpp"
#include "nodes/decode/decoder.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>

namespace zhulong {

/**
 * @brief 昇腾 VDEC 解码节点
 *
 * 线程模型：实例**不得跨线程使用**。ACL context 是线程当前态，
 * 通道在构造它的线程上创建，也必须在该线程上 send/receive/destroy。
 */
class AscendDecodeNode final : public IDecodeNode {
public:
    /** @brief 本机是否可用（编译期带 CANN + 运行期设备与符号探测通过） */
    [[nodiscard]] static bool is_supported() noexcept;

    /**
     * @brief 生产构造函数
     * @param width 编码图像宽度（像素）；VDEC 建通道必须已知，<=0 直接抛错
     * @param height 编码图像高度（像素）
     * @throws std::invalid_argument 编码格式不支持或宽高非法
     * @throws std::runtime_error 通道创建失败
     */
    AscendDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size, int32_t time_base_num,
                     int32_t time_base_den, uint64_t stream_id, int32_t width, int32_t height);

    /**
     * @brief 测试注入构造函数（不触碰 CANN，用假通道驱动状态机）
     *
     * ⚠️ 仅供单测使用：真实链路由工厂 + 生产构造函数装配。
     */
    AscendDecodeNode(ascend::IVdecChannel::Ptr channel, uint64_t stream_id, int32_t time_base_num,
                     int32_t time_base_den);

    ~AscendDecodeNode() override;

    int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;
    int receive_frame(HardwareFrame::Ptr &out_frame) override;
    void flush() override;

private:
    struct HostBufferPool;

    /** @brief 主机 NV12 帧池：按需分配、按帧归还，池随最后一个在途帧一起消亡 */
    std::shared_ptr<HostBufferPool> pool_;
    ascend::IVdecChannel::Ptr channel_;
    uint64_t stream_id_{0};
    int32_t time_base_num_{1};
    int32_t time_base_den_{90000};
    bool eos_sent_{false};
};

} // namespace zhulong
