/**
 * @file audio_normalizer.hpp
 * @brief 音频时间戳归一化器（锚定至视频输出时间轴）
 *
 * 核心机制：
 * 1. 共享视频输出时间轴：
 *    - 视频原点建立时刻 V 为基准点；
 *    - 视频原点建立后的首枚有效音频包到达时刻 a、媒体时间戳 A 构造基准锚点 base = a - V；
 *    - 逐包映射：audio_out_us = (audio_abs_us - A) + base + video_shift_us；
 * 2. 纯算术无锁设计：
 *    - 本类不调用任何系统读钟函数，不加互斥锁，所有时间由调用方单调时钟传入，
 *      与拉流 Worker 单线程上下文亲和，单测完全可确定性复现；
 * 3. 边界鲁棒性：
 *    - 视频首帧未到（origin_ready == false）前到达的音频包一律丢弃并计数；
 *    - 缺失 PTS 时按音频格式与采样率合成递推时间戳，并置 ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC；
 *    - 锚点若建在合成时间轴上（首包缺 PTS），首枚真实 PTS 到达时以它重锚一次，
 *      绝不把输出推到「真实 PTS 的绝对值」——否则时间轴会前跳整场；
 *    - 检测视频 discontinuity generation 跳变时置 ZHULONG_PKT_FLAG_DISCONTINUITY；
 *    - 源流缺少 DTS 时置 ZHULONG_PKT_FLAG_DTS_SYNTHETIC（dts_us 由 pts 合成）；
 *    - 强制单调性保护：若媒体 PTS 发生负向回退，标记 DISCONTINUITY 且绝不回拨时间轴。
 */

#pragma once

#include "Zhulong/engine.h"

#include <cstdint>

namespace zhulong {

class AudioNormalizer {
public:
    AudioNormalizer() = default;

    /**
     * @brief 更新音频流元数据配置
     */
    void update_metadata(int32_t codec, int32_t sample_rate, int32_t channels,
                         int32_t time_base_num, int32_t time_base_den);

    /**
     * @brief 归一化音频数据包时间戳
     *
     * @param in 原始音频包视图
     * @param video_origin_ready 视频原点是否已就绪
     * @param video_origin_arrival_us 视频首包到达时刻（微秒，steady_clock）
     * @param video_shift_us 当前视频时间轴跳变累计偏移（微秒）
     * @param video_discontinuity_gen 视频时间轴不连续代数
     * @param audio_arrival_us 当前音频包到达时刻（微秒，steady_clock）
     * @return Zhulong_packet_view 归一化后的包视图；若在视频就绪前被丢弃则 size 返回 0
     */
    Zhulong_packet_view normalize(const Zhulong_packet_view &in,
                                  bool video_origin_ready,
                                  int64_t video_origin_arrival_us,
                                  int64_t video_shift_us,
                                  uint64_t video_discontinuity_gen,
                                  int64_t audio_arrival_us);

    /** @brief 因视频原点尚未建立而丢弃的音频包数 */
    uint64_t dropped_before_origin() const { return dropped_before_origin_; }

    /** @brief 是否已成功建立音画对齐锚点 */
    bool anchor_established() const { return anchor_established_; }

    /** @brief 锚点基准偏移（微秒，base = a - V） */
    int64_t base_offset_us() const { return base_offset_us_; }

private:
    static int64_t rescale_to_microseconds(int64_t ticks, int32_t num, int32_t den);

    int32_t codec_{0};
    int32_t sample_rate_{8000};
    int32_t channels_{1};
    int32_t time_base_num_{1};
    int32_t time_base_den_{8000};

    bool anchor_established_{false};
    /** 锚点是否取自真实 PTS（false = 建在合成时钟上，等待首枚真实 PTS 重锚一次） */
    bool anchor_from_real_pts_{false};
    int64_t anchor_abs_us_{0};
    int64_t base_offset_us_{0};

    bool has_last_abs_{false};
    int64_t last_abs_us_{0};
    int64_t wrap_offset_us_{0};

    uint64_t last_video_gen_{0};
    bool has_last_out_{false};
    int64_t last_out_us_{0};

    uint64_t dropped_before_origin_{0};
};

} // namespace zhulong
