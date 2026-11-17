/**
 * @file fmp4_writer.hpp
 * @brief 事件录像片段的 fMP4 写出节点（原样 remux，不转码）
 *
 * 契约：
 * 1. 先写 `<path>.part`，finish() 成功后 rename 成 `<path>`：半个文件永远不会以完成品的名字出现；
 *    任何 FFmpeg 错误 / abort() / 未 finish 即析构 → 删除 `.part`。
 * 2. 容器参数 `movflags=frag_keyframe+empty_moov+default_base_moof`：每个关键帧切一个分片，
 *    写到一半的文件里已落盘的分片同样可播放。
 * 3. 输入为 Annex B 码流（PreviewNormalizer 的整形结果）。extradata 取首个关键帧内的
 *    参数集（H.264: SPS/PPS；H.265: VPS/SPS/PPS），以 Annex B 形式交给 mov muxer，
 *    由它转换成 avcC/hvcC，并把每个样本的起始码改写为长度前缀（单测读回验证）。
 * 4. 时间基 1/1000000：pts/dts 以首包 dts_us 为 0 点重基。
 *
 * ⚠️ 线程模型：单实例只允许单线程访问（录像写线程），内部不加锁。
 */

#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

struct AVFormatContext;
struct AVPacket;

namespace zhulong {

/** @brief 片段写出参数 */
struct Fmp4WriterConfig {
    std::string path;                            /**< 最终文件绝对路径（实际先写 path + ".part"） */
    int32_t codec{0};                            /**< Zhulong_CODEC_H264 / Zhulong_CODEC_H265 */
    int32_t width_hint{0};                       /**< 码流解析不出尺寸时的兜底宽度（流元数据） */
    int32_t height_hint{0};                      /**< 码流解析不出尺寸时的兜底高度 */
    std::vector<uint8_t> parameter_sets_hint;    /**< 首个关键帧不含参数集时的兜底（Annex B） */
    int64_t pts_reorder_offset_us{0};            /**< B 帧重排导致 pts < dts 时的正偏移，保持相对呈现序 */
};

/** @brief 已完成片段的统计 */
struct Fmp4WriterStats {
    int64_t packets{0};          /**< 成功写入的样本数 */
    int64_t first_pts_us{0};     /**< 首个样本的 pts_us（归一化时间轴） */
    int64_t last_pts_us{0};      /**< 最后一个样本的 pts_us */
    int64_t first_dts_us{0};     /**< 首个样本的 dts_us */
    int64_t last_dts_us{0};      /**< 最后一个样本的 dts_us */
    int64_t size_bytes{0};       /**< finish() 成功后的文件字节数 */
};

class Fmp4Writer {
public:
    explicit Fmp4Writer(Fmp4WriterConfig config);
    /** @brief 未 finish 的写出器析构即视为失败：关闭并删除 `.part` */
    ~Fmp4Writer();

    Fmp4Writer(const Fmp4Writer &) = delete;
    Fmp4Writer &operator=(const Fmp4Writer &) = delete;
    Fmp4Writer(Fmp4Writer &&) = delete;
    Fmp4Writer &operator=(Fmp4Writer &&) = delete;

    /**
     * @brief 写入一个视频样本（首次调用时打开容器）
     *
     * 第一个样本必须是关键帧，否则直接失败（片段必须能从首帧解码）。
     * @return false 表示写出器已进入失败态（后续调用都返回 false）
     */
    bool write(int64_t pts_us, int64_t dts_us, bool key_frame, const uint8_t *data, size_t size) noexcept;

    /**
     * @brief 写 trailer、关闭文件并 rename 为最终名
     * @return false 表示失败（`.part` 已删除）
     */
    bool finish() noexcept;

    /** @brief 放弃写出：关闭并删除 `.part`（幂等） */
    void abort() noexcept;

    bool failed() const noexcept { return failed_; }
    const Fmp4WriterStats &stats() const noexcept { return stats_; }
    const std::string &part_path() const noexcept { return part_path_; }

private:
    bool open(const uint8_t *first_key_frame, size_t size) noexcept;
    void close_context() noexcept;
    void remove_part() noexcept;

    Fmp4WriterConfig config_;
    std::string part_path_;
    AVFormatContext *context_{nullptr};
    AVPacket *packet_{nullptr};
    bool opened_{false};
    bool header_written_{false};
    bool failed_{false};
    bool finished_{false};
    int64_t pts_reorder_offset_us_{0};
    Fmp4WriterStats stats_{};
};

/**
 * @brief 从 Annex B 载荷里抽出参数集 NAL（H.264: 7/8；H.265: 32/33/34），以 4 字节起始码拼接
 * @return 未找到任何参数集时返回空
 */
std::vector<uint8_t> extract_annex_b_parameter_sets(int32_t codec, const uint8_t *data, size_t size);

} // namespace zhulong
