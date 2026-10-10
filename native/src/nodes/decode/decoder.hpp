/**
 * @file decoder.hpp
 * @brief 异构解码节点抽象接口定义
 */

#pragma once

#include "Zhulong/engine.h"
#include "Zhulong/frame.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>

namespace zhulong {

/** @brief 解码器后端类型 */
enum class DecoderBackend {
    AUTO = 0,       /**< 自动选择后端 (优先硬件，无硬件时回退软解) */
    FFMPEG_SW = 1,  /**< 静态 FFmpeg 软解 (通用 CPU 保底) */
};

/**
 * @brief 统一解码节点纯虚接口
 */
class IDecodeNode {
public:
    using Ptr = std::unique_ptr<IDecodeNode>;
    virtual ~IDecodeNode() = default;

    /**
     * @brief 送入待解码的压缩数据包 (非阻塞)
     * @param data 数据首地址
     * @param size 数据长度
     * @param pts 显示时间戳
     * @param dts 解码时间戳
     * @return 0 成功; <0 错误码
     */
    virtual int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) = 0;

    /**
     * @brief 拉取解出的硬件帧
     * @param[out] out_frame 输出帧
     * @return 0 成功获取一帧; 1 暂无输出需继续喂包; <0 发生解码错误
     */
    virtual int receive_frame(HardwareFrame::Ptr &out_frame) = 0;

    /**
     * @brief 重置/刷新解码器状态 (清空 DPB 参考帧)
     */
    virtual void flush() = 0;

    /**
     * @brief 创建解码节点实例
     */
    static Ptr create(int32_t codec, const uint8_t *extradata = nullptr, size_t extradata_size = 0,
                      DecoderBackend backend = DecoderBackend::AUTO);
};

} // namespace zhulong
