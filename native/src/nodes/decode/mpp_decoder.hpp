/**
 * @file mpp_decoder.hpp
 * @brief Rockchip MPP 硬件解码节点抽象定义
 *
 * 实现 IDecodeNode 纯虚接口，基于 Rockchip MPP 输出携带 DMA-BUF fd 的 HardwareFrame。
 */

#pragma once

#include "nodes/decode/decoder.hpp"
#include "nodes/decode/mpp_loader.hpp"

#include <memory>

namespace zhulong {

class MppDecodeNode final : public IDecodeNode {
public:
    static bool is_supported() noexcept;

    /**
     * @brief 构造硬解节点
     * @param codec 视频编码格式（Zhulong_CODEC_H264 或 Zhulong_CODEC_H265）
     * @param extradata 视频参数集二进制数据指针（SPS/PPS/VPS），可为空
     * @param extradata_size 参数集字节数
     * @param time_base_num 时间基分子
     * @param time_base_den 时间基分母
     * @param stream_id 用于原生日志关联的稳定流 ID；未提供时为 0
     * @throws std::invalid_argument 不支持的编码格式
     * @throws std::runtime_error 宿主不支持 MPP 或解码器初始化失败
     */
    MppDecodeNode(int32_t codec, const uint8_t *extradata = nullptr, size_t extradata_size = 0,
                  int32_t time_base_num = 1, int32_t time_base_den = 90000, uint64_t stream_id = 0);
    ~MppDecodeNode() override;

    int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;
    int receive_frame(HardwareFrame::Ptr &out_frame) override;
    void flush() override;

private:
    /**
     * @brief 外部缓冲组的引用计数持有者
     *
     * ⚠️ 缓冲组的销毁时机由「最后一张引用它的 HardwareFrame」决定，而不是解码器实例本身：
     * 解码器可能在帧尚未归还时被销毁（停机）或重新配置（分辨率切换）。若在解码器析构/重建时
     * 无条件 `mpp_buffer_group_clear/put`，在途帧持有的 MppBuffer 会指向已释放的缓冲组，
     * 后续 `mpp_buffer_put` 即为 use-after-free。
     */
    struct BufferGroup {
        BufferGroup() = default;
        ~BufferGroup();

        BufferGroup(const BufferGroup &) = delete;
        BufferGroup &operator=(const BufferGroup &) = delete;

        mpp::MppLoader *loader{nullptr};
        mpp::MppBufferGroup raw{nullptr};
        uint64_t stream_id{0};
    };

    /** @brief 初始化入口：异常时回收半初始化的 MPP 句柄（构造函数抛出不会运行析构） */
    void init_mpp(int32_t codec, const uint8_t *extradata, size_t extradata_size);
    void setup_mpp(int32_t codec, const uint8_t *extradata, size_t extradata_size);
    void destroy_mpp() noexcept;

    /** @brief 处理 info_change：新建外部缓冲组并通知 MPP 解除挂起 */
    int configure_buffer_group(mpp::MppFrame frame);

    /** @brief 统一下发 MPP 控制命令并记录失败原因（成功返回 decode_ok） */
    int control(int32_t cmd, void *param, const char *what) noexcept;

    mpp::MppLoader &loader_;
    mpp::MppCtx ctx_{nullptr};
    mpp::MppApi *mpi_{nullptr};
    std::shared_ptr<BufferGroup> buf_group_;

    uint64_t stream_id_{0};
    int32_t time_base_num_{1};
    int32_t time_base_den_{90000};

    bool initialized_{false};
    bool eos_sent_{false};
    bool unsupported_format_logged_{false};
};

} // namespace zhulong
