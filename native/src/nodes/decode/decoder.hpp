/**
 * @file decoder.hpp
 * @brief 异构解码节点抽象接口定义
 *
 * 定义跨平台软硬件解码节点的纯虚接口契约。
 * 遵循非阻塞流式解码规范（Send-Packet / Receive-Frame），输出携带独立 RAII 释放闭包的 HardwareFrame。
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
    AUTO = 0,       /**< 自动选择后端 (优先硬件加速，无硬件时回退软解) */
    FFMPEG_SW = 1,  /**< 静态 FFmpeg 软解 (通用 CPU 保底) */
};

/**
 * @brief 统一解码节点纯虚接口
 *
 * 规范解码器的两段式流式处理契约：
 * 1. 生产者通过 send_packet() 喂入压缩数据；
 * 2. 消费者通过 receive_frame() 轮询拉取解码帧，直至返回 1 (EAGAIN)；
 * 3. 发生严重跳帧或重同步时，通过 flush() 冲刷参考帧缓冲。
 */
class IDecodeNode {
public:
    using Ptr = std::unique_ptr<IDecodeNode>;
    virtual ~IDecodeNode() = default;

    /**
     * @brief 送入待解码的压缩数据包 (非阻塞)
     *
     * ⚠️ 排空（Drain）信号：
     *   - 当 data 为 nullptr 或 size 为 0 时，视为发送冲刷排空标记，通知底层解码器输出所有驻留帧。
     *
     * @param data 压缩数据包借用指针
     * @param size 数据包字节长度
     * @param pts 源流显示时间戳
     * @param dts 源流解码时间戳
     * @return int 0 成功入队；<0 底层解码器返回的错误码
     */
    virtual int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) = 0;

    /**
     * @brief 拉取解出的硬件帧
     *
     * ⚠️ EAGAIN 排空契约：
     *   - 返回 1 表示当前输入包已被消化，但暂无新帧产出，需继续喂入下一个数据包；
     *   - 返回 0 表示成功获取一帧，调用方应在循环中继续调用直至返回 1，彻底排空当前就绪的所有帧。
     *
     * @param[out] out_frame 输出装配完成的硬件帧对象指针 (包含专属 release_fn)
     * @return int 0 成功获取一帧；1 暂无输出需继续喂包；<0 发生不可恢复的解码错误
     */
    virtual int receive_frame(HardwareFrame::Ptr &out_frame) = 0;

    /**
     * @brief 冲刷重置解码器状态 (清空 DPB 参考帧缓冲)
     *
     * 适用于丢包恢复、关键帧重同步或流切换场景。
     */
    virtual void flush() = 0;

    /**
     * @brief 创建解码节点实例的静态工厂方法
     *
     * @param codec 视频编码格式（Zhulong_CODEC_H264 或 Zhulong_CODEC_H265）
     * @param extradata 视频头/参数集二进制数据借用指针（SPS/PPS/VPS）
     * @param extradata_size 参数集字节长度
     * @param time_base_num 源流时间基分子
     * @param time_base_den 源流时间基分母
     * @param backend 期望的解码器后端类型
     * @return Ptr 创建成功的解码节点唯一智能指针
     */
    static Ptr create(int32_t codec, const uint8_t *extradata = nullptr, size_t extradata_size = 0,
                      int32_t time_base_num = 1, int32_t time_base_den = 90000,
                      DecoderBackend backend = DecoderBackend::AUTO);
};

} // namespace zhulong
