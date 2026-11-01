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

#include <cerrno>
#include <cstddef>
#include <cstdint>
#include <memory>

namespace zhulong {

/** @brief 解码器后端类型 */
enum class DecoderBackend {
    automatic = 0, /**< 自动选择后端 (优先硬件加速，无硬件时回退软解) */
    ffmpeg_sw = 1, /**< 静态 FFmpeg 软解 (通用 CPU 保底) */
    mpp_hw = 2,    /**< Rockchip MPP 硬件解码器 (RK3568/RK3588 专用) */
};

/**
 * @brief 解码节点统一返回码
 *
 * `send_packet()` 与 `receive_frame()` 共用同一值域，取值与 FFmpeg `AVERROR(e)` 编码**逐个等值**
 * （`AVERROR(e) == -e`），因此调用方可直接与 `AVERROR(EAGAIN)` 比较，也可 `av_strerror` 打印。
 * 接口本身不引入 libavutil 头文件，保持抽象层不依赖具体后端。
 *
 * 硬件后端私有错误码（`MPP_ERR_*`）必须在后端内部归一化，严禁外溢给调用方 —— 否则
 * `MPP_NOK`(-1)、`MPP_ERR_BUFFER_FULL`(-1012) 等值会与 AVERROR 码产生语义混淆。
 */
constexpr int decode_ok = 0;                                   /**< 成功（成功入队 / 取到一帧） */
constexpr int decode_again = -EAGAIN;                          /**< 背压或暂无输出（== AVERROR(EAGAIN)） */
constexpr int decode_backend_failure = -EIO;                   /**< 后端不可恢复错误（== AVERROR(EIO)） */

/**
 * @brief 统一解码节点纯虚接口
 *
 * 规范解码器的两段式流式处理契约：
 * 1. 生产者通过 send_packet() 喂入压缩数据；
 * 2. 消费者通过 receive_frame() 轮询拉取解码帧，直至返回 decode_again；
 * 3. 发生严重跳帧或重同步时，通过 flush() 冲刷参考帧缓冲。
 */
class IDecodeNode {
public:
    using Ptr = std::unique_ptr<IDecodeNode>;

    virtual ~IDecodeNode() = default;

    // ⚠️ 多态解码节点禁止拷贝与移动：副本会共享同一份底层解码器上下文，
    // 导致重复释放解码状态与内核句柄（Core Guidelines C.67）
    IDecodeNode(const IDecodeNode &) = delete;
    IDecodeNode &operator=(const IDecodeNode &) = delete;
    IDecodeNode(IDecodeNode &&) = delete;
    IDecodeNode &operator=(IDecodeNode &&) = delete;

    /**
     * @brief 送入待解码的压缩数据包 (非阻塞)
     *
     * ⚠️ 排空（Drain）信号：
     *   - 当 data 为 nullptr 或 size 为 0 时，视为发送冲刷排空标记，通知底层解码器输出所有驻留帧。
     *
     * ⚠️ 背压与错误码契约：
     *   - 返回 decode_ok (0) 表示成功入队；
     *   - 返回 decode_again (AVERROR(EAGAIN)) 表示底层队列已满，调用方应排空已解码帧并重新重发当前包；
     *   - 返回 AVERROR_EOF 表示排空已完成，继续送包无效，必须先 flush()；
     *   - 返回其他 <0 表示底层解码器严重错误（`av_strerror` 可用），调用方应执行 flush() 并重同步关键帧。
     *
     * @param data 压缩数据包借用指针（仅在本次调用期间有效，解码器不得跨调用持有）
     * @param size 数据包字节长度
     * @param pts 源流显示时间戳（缺失时传 AV_NOPTS_VALUE，严禁用 0 冒充）
     * @param dts 源流解码时间戳（缺失时传 AV_NOPTS_VALUE；硬件后端可能不透传该字段）
     * @return int 统一返回码，见上
     */
    virtual int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) = 0;

    /**
     * @brief 拉取解出的硬件帧
     *
     * ⚠️ 排空契约：
     *   - 返回 decode_ok (0) 表示成功获取一帧，调用方应在循环中继续调用直至返回 decode_again，
     *     彻底排空当前就绪的所有帧；
     *   - 返回 decode_again (AVERROR(EAGAIN)) 表示当前输入已被消化但暂无新帧产出，需继续喂入下一个数据包；
     *   - ⚠️ 本接口**不区分**「暂无输出」(EAGAIN) 与「drain 已排空」(AVERROR_EOF)，两者都返回
     *     decode_again：drain（`send_packet(nullptr, 0, ...)`）之后若要继续送包，调用方**必须**
     *     先 flush() 复位 draining 状态，否则底层 send_packet 会恒返 AVERROR_EOF；
     *   - 返回其他 <0 表示不可恢复的解码错误。
     *
     * @param[out] out_frame 输出装配完成的硬件帧对象指针 (包含专属 release_fn)；失败时被置空
     * @return int 统一返回码，见上
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
     * @param stream_id 用于原生日志关联的稳定流 ID；未提供时为 0
     * @return Ptr 创建成功的解码节点唯一智能指针
     * @throws std::invalid_argument 不支持的编码格式
     * @throws std::runtime_error 指定后端不可用或后端初始化失败
     */
    static Ptr create(int32_t codec, const uint8_t *extradata = nullptr, size_t extradata_size = 0,
                      int32_t time_base_num = 1, int32_t time_base_den = 90000,
                      DecoderBackend backend = DecoderBackend::automatic, uint64_t stream_id = 0);

protected:
    /** @brief 仅允许派生类构造（拷贝/移动已显式删除） */
    IDecodeNode() = default;
};

} // namespace zhulong
