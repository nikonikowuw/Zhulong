/**
 * @file mpp_decoder.hpp
 * @brief Rockchip MPP 硬件解码节点抽象定义
 *
 * 实现 IDecodeNode 纯虚接口，基于 Rockchip MPP 输出携带 DMA-BUF fd 的 HardwareFrame。
 */

#pragma once

#include "decoder.hpp"
#include "mpp_loader.hpp"

namespace zhulong {

class MppDecodeNode : public IDecodeNode {
public:
    static bool is_supported() noexcept;

    MppDecodeNode(int32_t codec, const uint8_t *extradata = nullptr, size_t extradata_size = 0,
                  int32_t time_base_num = 1, int32_t time_base_den = 90000);
    ~MppDecodeNode() override;

    MppDecodeNode(const MppDecodeNode&) = delete;
    MppDecodeNode& operator=(const MppDecodeNode&) = delete;

    int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;
    int receive_frame(HardwareFrame::Ptr &out_frame) override;
    void flush() override;

private:
    void init_mpp(int32_t codec, const uint8_t *extradata, size_t extradata_size);
    void destroy_mpp() noexcept;
    int handle_info_change(mpp::MppFrame frame);

    mpp::MppLoader& loader_;
    mpp::MppCtx ctx_{nullptr};
    mpp::MppApi *mpi_{nullptr};
    mpp::MppBufferGroup buf_group_{nullptr};

    int32_t time_base_num_{1};
    int32_t time_base_den_{90000};

    uint32_t width_{0};
    uint32_t height_{0};
    uint32_t hor_stride_{0};
    uint32_t ver_stride_{0};

    bool initialized_{false};
    bool eos_sent_{false};
};

} // namespace zhulong
