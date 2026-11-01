/**
 * @file preview_normalizer.hpp
 * @brief 预览契约整形层（平台无关）
 *
 * 目标：把「浏览器能不能出画」这件事故意收敛到本层，让上层 Go / Web 前端不再需要
 * 对时间基、参数集位置、可丢帧性做任何假设。
 *
 * 三项职责：
 * 1. 时间戳归一化：
 *    - PTS 是显示时间，按源 time_base 换算为微秒并保留 B 帧重排顺序，不强制单调；
 *    - DTS 是解码时间，按源 time_base 换算并保证解码包序单调；缺失时生成单调合成值；
 *    - 源会话重建通过 DTS 回退或大幅跳变识别，并打上 DISCONTINUITY 标志。
 * 2. 参数集注入：
 *    - 从探测阶段得到的 extradata 转成 Annex B，在关键帧缺少参数集时前置拼接；
 *    - 拼接复用成员缓冲，只在关键帧（约 1~2 次/秒）触发，不引入每帧分配。
 * 3. 帧分类：
 *    - 标注 IDR / 参考帧 / 非参考帧，让接收方能安全地丢弃非参考帧而不破坏参考链；
 *    - 判定不确定时一律保守归为参考帧。
 *
 * ⚠️ 线程模型：本层**不加锁**，只能由单一拉流 Worker 线程访问（即 Stream::dispatch
 *    与 Stream::on_ready 所在线程）。这与 DecodeSession 的单生产者假设一致。
 * ⚠️ 生命周期：normalize 返回视图中的 data 可能指向内部 stitch 缓冲，**仅在下一次
 *    normalize 调用前有效**，与 engine.h 的「回调期间借用」契约完全一致。
 * ⚠️ 平台无关：本层不含任何 Rockchip / DMA / 内核依赖，RK3568 与 RK3588 共用同一份实现；
 *    平台差异只体现在资源配额（见 internal/config 的 preview profile），不体现在这里。
 */

#pragma once

#include "Zhulong/engine.h"

#include <cstdint>
#include <vector>

namespace zhulong {

/**
 * @brief 将 H.264/H.265 Annex B 或 codec configuration record 参数集统一为 Annex B
 *
 * 支持 Annex B、H.264 AVCDecoderConfigurationRecord (avcC)、H.265
 * HEVCDecoderConfigurationRecord (hvcC)，以及 4 字节长度前缀 NAL 序列。
 *
 * @param codec Zhulong_CODEC_H264 / Zhulong_CODEC_H265
 * @param extradata 原始参数集字节
 * @param size 字节长度
 * @return Annex B 形式的参数集；输入为空或格式非法时返回空
 */
std::vector<uint8_t> to_annex_b(int32_t codec, const uint8_t *extradata, size_t size);

/**
 * @brief 判断 Annex B 载荷内是否已自包含解码所需的参数集
 *
 * H.264 需同时具备 SPS(7) 与 PPS(8)；H.265 需同时具备 VPS(32)、SPS(33) 与 PPS(34)。
 * 缺失任意一项都认为需要注入，宁可重复注入也不能让解码器缺参数集。
 *
 * @param codec Zhulong_CODEC_H264 / Zhulong_CODEC_H265
 * @param data Annex B 载荷
 * @param size 载荷长度
 * @return true 表示已完整具备，无需注入
 */
bool has_complete_parameter_sets(int32_t codec, const uint8_t *data, size_t size);

/**
 * @brief 判断一个非关键帧是否为非参考帧（可安全丢弃）
 *
 * 只有当载荷中的全部 slice NAL 都标记为非参考时才返回 true；
 * 无法解析出任何 slice NAL 时返回 false（保守视为参考帧）。
 *
 * @param codec Zhulong_CODEC_H264 / Zhulong_CODEC_H265
 * @param data Annex B 载荷
 * @param size 载荷长度
 * @return true 表示可安全丢弃
 */
bool is_non_reference_frame(int32_t codec, const uint8_t *data, size_t size);

// 兼容别名 (对齐 naming-guidelines §4 规范化)
inline std::vector<uint8_t> ToAnnexB(int32_t codec, const uint8_t *extradata, size_t size) {
    return to_annex_b(codec, extradata, size);
}
inline bool HasCompleteParameterSets(int32_t codec, const uint8_t *data, size_t size) {
    return has_complete_parameter_sets(codec, data, size);
}
inline bool IsNonReferenceFrame(int32_t codec, const uint8_t *data, size_t size) {
    return is_non_reference_frame(codec, data, size);
}

/**
 * @brief 预览契约整形器
 */
class PreviewNormalizer {
public:
    PreviewNormalizer() = default;

    /**
     * @brief 重置全部状态（流重建时调用，使新会话从干净基准开始）
     */
    void reset();

    /**
     * @brief 更新流元数据
     *
     * 由 Stream::on_ready 在探测完成后调用；extradata 在此处完成 Annex B 归一化并持久化。
     *
     * @param codec 编解码类型
     * @param extradata 参数集原始字节（可为空）
     * @param extradata_size 参数集长度
     * @param time_base_num 流时间基分子（<=0 时回退为 1）
     * @param time_base_den 流时间基分母（<=0 时回退为 90000）
     */
    void update_metadata(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                         int32_t time_base_num, int32_t time_base_den);

    /**
     * @brief 将一个原始数据包整形为预览契约视图
     *
     * 除 data 可能指向内部缓冲外，其余字段均为输入字段的安全副本，
     * 原始 pts / dts / time_base / codec 一律保真透传。
     *
     * @param in 来自 RTSP 读取循环的原始包视图
     * @return 整形后的视图（data 借用自内部缓冲，下次调用前有效）
     */
    Zhulong_packet_view normalize(const Zhulong_packet_view &in);

    /** @brief 当前 GOP 序号（仅诊断与测试使用） */
    uint32_t gop_id() const { return gop_id_; }

    /** @brief 参数集注入累计次数（仅诊断与测试使用） */
    uint64_t injected_count() const { return injected_count_; }

    /** @brief 时间轴跳变累计次数（仅诊断与测试使用） */
    uint64_t discontinuity_count() const { return discontinuity_count_; }

private:
    /** @brief 将相对源时间戳换算为微秒，使用 128 位中间量防溢出 */
    static int64_t rescale_delta_to_microseconds(int64_t ticks, int64_t origin,
                                                 int32_t num, int32_t den);

    int32_t codec_{0};
    int32_t time_base_num_{1};
    int32_t time_base_den_{90000};

    std::vector<uint8_t> parameter_sets_;   /**< Annex B 形式的参数集，仅在 update_metadata 时重建 */
    std::vector<uint8_t> stitch_;           /**< 参数集注入的复用缓冲，避免每关键帧分配 */

    bool origin_set_{false};
    int64_t origin_ticks_{0};
    int64_t timestamp_shift_us_{0};
    bool has_last_source_clock_{false};
    int64_t last_source_clock_us_{0};
    bool last_source_clock_has_dts_{false};
    bool has_last_dts_{false};
    int64_t last_dts_us_{0};

    uint32_t gop_id_{0};
    uint64_t injected_count_{0};
    uint64_t discontinuity_count_{0};
};

} // namespace zhulong
