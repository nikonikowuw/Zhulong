/**
 * @file ffmpeg_decoder.hpp
 * @brief 基于静态 FFmpeg 的 CPU 软解节点定义
 *
 * 核心设计：
 * 1. 静态单线程软解：thread_count 固定为 1，杜绝线程爆炸与内部状态不同步；
 * 2. 内存复用与借用：AVPacket 句柄在实例生命周期内复用，送包时仅挂接借用指针，调用后立即脱钩，
 *    确保 `av_packet_unref` 永远不会尝试释放调用方的只读内存（libavcodec 内部仍会做一次受控拷贝）；
 * 3. 硬件帧装配：将 FFmpeg 解出的 AVFrame 装配为统一的 HardwareFrame，并注入安全释放闭包。
 */

#pragma once

#include "nodes/decode/decoder.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
}

namespace zhulong {

/**
 * @brief 基于 FFmpeg 静态库的确定性软件解码实现
 */
class FFmpegDecodeNode final : public IDecodeNode {
public:
    /**
     * @brief 构造软解节点
     * @param codec 视频编码格式（Zhulong_CODEC_H264 或 Zhulong_CODEC_H265）
     * @param extradata 视频参数集二进制数据指针（SPS/PPS/VPS）
     * @param extradata_size 参数集字节数
     * @param time_base_num 时间基分子
     * @param time_base_den 时间基分母
     * @param stream_id 用于原生日志关联的稳定流 ID；未提供时为 0
     * @throws std::invalid_argument 不支持的编码格式
     * @throws std::runtime_error 未找到解码器或打开解码上下文失败
     * @throws std::bad_alloc 内存不足
     */
    FFmpegDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                     int32_t time_base_num = 1, int32_t time_base_den = 90000, uint64_t stream_id = 0);

    /** @brief 析构函数，释放复用的 AVPacket 与 AVCodecContext 资源 */
    ~FFmpegDecodeNode() override;

    /**
     * @brief 送入待解码的压缩数据包
     * @param data 压缩数据包借用指针（为 nullptr 或 size=0 时发送排空标记）
     * @param size 数据长度
     * @param pts 显示时间戳
     * @param dts 解码时间戳
     * @return int decode_ok；decode_again；AVERROR_EOF；其余 <0 为 AVERROR 错误码
     */
    int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;

    /**
     * @brief 拉取已解码完成的一帧
     * @param[out] out_frame 输出装配完成的 HardwareFrame 共享指针
     * @return int decode_ok 取到一帧；decode_again 暂无输出；<0 解码失败
     */
    int receive_frame(HardwareFrame::Ptr &out_frame) override;

    /**
     * @brief 冲刷重置解码器缓冲区
     */
    void flush() override;

private:
    AVCodecContext *codec_ctx_{nullptr}; /**< FFmpeg 解码器上下文 */
    AVPacket *packet_{nullptr};          /**< 复用的输入包容器句柄 */
    int32_t time_base_num_{1};           /**< 源流时间基分子 */
    int32_t time_base_den_{90000};       /**< 源流时间基分母 */
    uint64_t stream_id_{0};              /**< 原生日志关联的流 ID */
    bool unsupported_format_logged_{false}; /**< 未知像素格式仅告警一次，避免低帧率流刷屏 */
};

} // namespace zhulong
