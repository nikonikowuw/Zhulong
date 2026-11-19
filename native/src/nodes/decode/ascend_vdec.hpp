/**
 * @file ascend_vdec.hpp
 * @brief 昇腾 DVPP VDEC 通道抽象（CANN-free 接缝）
 *
 * 设计约束（详见 .trellis/spec/native/hardware-backends.md §8）：
 * 1. CANN 类型与头文件**只允许**出现在 `ascend_channel.cpp` 一个编译单元里。本头文件不包含任何
 *    CANN/AscendCL 声明，因此解码节点逻辑可以在没有 CANN 工具链的宿主机上编译与单测；
 * 2. 通道层与 `IDecodeNode` 的 send/receive 语义一一对应：`hi_mpi_vdec_*` 是拉取式接口，
 *    没有回调线程、没有 userData 生命周期、没有描述符归属问题（这是选择它而非 `aclvdec*` 的理由）；
 * 3. 返回码复用 `decoder.hpp` 的 `decode_ok / decode_again / decode_backend_failure`，
 *    后端私有错误码（`HI_ERR_VDEC_*`）必须在通道层归一化，严禁外溢。
 */

#pragma once

#include "nodes/decode/decoder.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>

namespace zhulong::ascend {

/** @brief 支持的视频编码格式（与 `hi_payload_type` 的映射在通道实现内完成） */
enum class VdecCodec {
    h264 = 0,
    h265 = 1,
};

/**
 * @brief 输出帧几何（含硬件对齐后的真实步长）
 *
 * `width_stride` / `height_stride` 以解码器实际产出为准（`hi_video_frame.width_stride[0]`），
 * 不是请求值：驱动可能选择更大的对齐粒度。
 */
struct VdecGeometry {
    uint32_t width{0};
    uint32_t height{0};
    uint32_t width_stride{0};
    uint32_t height_stride{0};
    size_t buffer_size{0}; /**< NV12 半平面连续布局的总字节数（Y + UV 在同一块设备内存） */
};

/** @brief 建通道参数（VDEC 通道创建时必须已知编码宽高，无法事后调整） */
struct VdecChannelConfig {
    VdecCodec codec{VdecCodec::h264};
    uint32_t width{0};
    uint32_t height{0};
    uint32_t ref_frame_num{8};     /**< 参考帧数（H.264 建议 8，H.265 建议 8~16） */
    uint32_t display_frame_num{2}; /**< 显示延迟帧数 */
    uint64_t stream_id{0};         /**< 仅用于日志关联 */
};

/** @brief 一帧解码结果（设备内存仍归通道所有，必须 `release()` 归还） */
struct DecodedFrame {
    void *dev_ptr{nullptr}; /**< 设备虚拟地址（`hi_video_frame.virt_addr[0]`），NV12 连续布局 */
    int64_t pts{0};
    bool has_pts{false};
    VdecGeometry geometry;
};

/**
 * @brief 昇腾 VDEC 通道接口
 *
 * 生命周期：`create_vdec_channel()` 返回的实例独占一个 `hi_mpi_vdec` 通道号与一个 AscendCL context，
 * 必须在**创建它的线程**上使用（ACL context 是线程当前态，不是句柄）。
 */
class IVdecChannel {
public:
    using Ptr = std::unique_ptr<IVdecChannel>;

    virtual ~IVdecChannel() = default;

    IVdecChannel(const IVdecChannel &) = delete;
    IVdecChannel &operator=(const IVdecChannel &) = delete;
    IVdecChannel(IVdecChannel &&) = delete;
    IVdecChannel &operator=(IVdecChannel &&) = delete;

    /**
     * @brief 送入一帧压缩码流（Annex-B Access Unit）
     * @return decode_ok / decode_again（通道码流缓冲已满）/ decode_backend_failure
     */
    virtual int send(const uint8_t *data, size_t size, int64_t pts, int64_t dts) = 0;

    /** @brief 送入 EOS 标记，触发解码器吐完所有驻留帧 */
    virtual int send_eos() = 0;

    /**
     * @brief 非阻塞拉取一帧
     * @return decode_ok（`out.dev_ptr` 有效）/ decode_again（暂无输出）/ decode_backend_failure
     */
    virtual int receive(DecodedFrame &out) = 0;

    /** @brief 归还 `receive()` 借出的设备帧（幂等：未知指针直接忽略） */
    virtual void release(void *dev_ptr) noexcept = 0;

    /** @brief 冲刷参考帧（stop_recv_stream + reset_chn + start_recv_stream） */
    virtual void reset() = 0;

    /** @brief 设备内存 → 主机内存拷贝（`aclrtMemcpy`，D2H） */
    virtual int copy_to_host(void *host, const void *dev, size_t size) = 0;

    /** @brief 通道是否仍可继续使用；一旦发生不可恢复错误即置 false */
    [[nodiscard]] virtual bool healthy() const noexcept = 0;

protected:
    IVdecChannel() = default;
};

/**
 * @brief 计算 NV12 输出几何（纯函数，可单测）
 *
 * 对齐规则（DVPP 要求）：宽 stride 16 对齐且不小于 32；高 stride 2 对齐且不小于图像高度；
 * 缓冲大小 = width_stride * height_stride * 3 / 2（YUV420SP 的 Y 与 UV 连续存放）。
 */
[[nodiscard]] VdecGeometry compute_vdec_geometry(uint32_t width, uint32_t height) noexcept;

/** @brief 本次构建是否带上了 CANN 头文件（`ZHULONG_HAVE_ASCEND`） */
[[nodiscard]] bool ascend_vdec_compiled() noexcept;

/** @brief 本次构建是否带 CANN 且运行期探测到可用设备与全部符号 */
[[nodiscard]] bool ascend_vdec_available() noexcept;

/**
 * @brief 创建真实通道
 * @return 成功返回独占通道；未编译 CANN、设备缺失、符号缺失或建通道失败时返回 nullptr
 */
[[nodiscard]] IVdecChannel::Ptr create_vdec_channel(const VdecChannelConfig &config);

} // namespace zhulong::ascend
