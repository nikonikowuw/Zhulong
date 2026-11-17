/**
 * @file clip_recorder.cpp
 * @brief 事件录像片段：GOP 环形缓冲、片段状态机与写线程实现
 */

#include "pipeline/clip_recorder.hpp"

#include "common/log.hpp"
#include "nodes/capture/rtsp_input.hpp"
#include "nodes/record/fmp4_writer.hpp"

#include <algorithm>
#include <limits>
#include <new>
#include <utility>

namespace zhulong {

namespace {

constexpr const char *log_module = "pipeline.clip";

/** @brief 生产写出器：fMP4（`.part` → rename） */
class Fmp4ClipFileWriter final : public ClipFileWriter {
public:
    explicit Fmp4ClipFileWriter(const ClipWriteRequest &request)
        : writer_(Fmp4WriterConfig{request.path, request.codec, request.width_hint, request.height_hint,
                                   request.parameter_sets_hint, request.pts_reorder_offset_us}) {}

    bool write(const ClipSample &sample) noexcept override {
        return writer_.write(sample.pts_us, sample.dts_us, sample.key_frame, sample.bytes.data(),
                             sample.bytes.size());
    }

    bool finish(int64_t *out_size_bytes) noexcept override {
        if (!writer_.finish()) return false;
        if (out_size_bytes != nullptr) *out_size_bytes = writer_.stats().size_bytes;
        return true;
    }

    void abort() noexcept override { writer_.abort(); }

private:
    Fmp4Writer writer_;
};

int64_t positive_or(int64_t value, int64_t fallback) noexcept {
    return value > 0 ? value : fallback;
}

int64_t saturating_add(int64_t left, int64_t right) noexcept {
    if (right > 0 && left > std::numeric_limits<int64_t>::max() - right) {
        return std::numeric_limits<int64_t>::max();
    }
    if (right < 0 && left < std::numeric_limits<int64_t>::min() - right) {
        return std::numeric_limits<int64_t>::min();
    }
    return left + right;
}

int64_t saturating_sub(int64_t left, int64_t right) noexcept {
    if (right < 0 && left > std::numeric_limits<int64_t>::max() + right) {
        return std::numeric_limits<int64_t>::max();
    }
    if (right > 0 && left < std::numeric_limits<int64_t>::min() + right) {
        return std::numeric_limits<int64_t>::min();
    }
    return left - right;
}

int64_t ms_to_us_saturating(int64_t ms) noexcept {
    if (ms <= 0) return 0;
    constexpr int64_t max_ms = std::numeric_limits<int64_t>::max() / 1000;
    if (ms >= max_ms) return std::numeric_limits<int64_t>::max();
    return ms * 1000;
}

} // namespace

ClipWriterFactory make_fmp4_writer_factory() {
    return [](const ClipWriteRequest &request) -> std::unique_ptr<ClipFileWriter> {
        return std::make_unique<Fmp4ClipFileWriter>(request);
    };
}

// ============================================================================
//                               ClipEventQueue
// ============================================================================

ClipEventQueue::ClipEventQueue(size_t capacity) : capacity_(capacity > 0 ? capacity : 1) {}

uint64_t ClipEventQueue::next_sequence() noexcept {
    return next_seq_.fetch_add(1, std::memory_order_relaxed);
}

void ClipEventQueue::push(const ClipEvent &event) noexcept {
    std::lock_guard<std::mutex> lock(mutex_);
    try {
        if (events_.size() >= capacity_) {
            events_.pop_front();
            dropped_.fetch_add(1, std::memory_order_relaxed);
        }
        events_.push_back(event);
    } catch (...) {
        // deque 扩容失败只丢这一条：与「溢出丢事件」同一兜底（Go 启动修正）
        dropped_.fetch_add(1, std::memory_order_relaxed);
    }
}

size_t ClipEventQueue::poll(ClipEvent *out, size_t capacity) noexcept {
    if (out == nullptr || capacity == 0) return 0;
    std::lock_guard<std::mutex> lock(mutex_);
    size_t count = 0;
    while (count < capacity && !events_.empty()) {
        out[count++] = events_.front();
        events_.pop_front();
    }
    return count;
}

// ============================================================================
//                               ClipRecorder
// ============================================================================

ClipRecorder::ClipRecorder(Zhulong_stream_id stream_id, const RecorderSettings &settings,
                           std::shared_ptr<ClipEventQueue> events, ClipWriterFactory writer_factory)
    : stream_id_(stream_id), events_(std::move(events)), writer_factory_(std::move(writer_factory)) {
    if (!events_ || !writer_factory_) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    configure(settings);
    writer_ = std::thread(&ClipRecorder::writer_loop, this);
}

ClipRecorder::~ClipRecorder() {
    shutdown();
}

void ClipRecorder::configure(const RecorderSettings &settings) {
    if (settings.pre_ms <= 0) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    std::lock_guard<std::mutex> lock(mutex_);
    settings_.pre_ms = settings.pre_ms;
    settings_.byte_cap = positive_or(settings.byte_cap, default_clip_buffer_byte_cap);
    settings_.queue_byte_cap = positive_or(settings.queue_byte_cap, default_clip_queue_byte_cap);
    {
        std::lock_guard<std::mutex> queue_lock(queue_mutex_);
        queue_byte_cap_ = settings_.queue_byte_cap;
    }
    evict_locked();
}

ClipRecorder::Snapshot ClipRecorder::snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    Snapshot out{};
    out.gop_count = gops_.size();
    for (const auto &gop : gops_) out.sample_count += gop.samples.size();
    out.buffered_bytes = buffered_bytes_;
    out.oldest_pts_us = gops_.empty() ? 0 : gops_.front().start_pts_us;
    out.newest_pts_us = newest_pts_us_;
    out.active = active_.has_value();
    out.truncations = truncations_;
    return out;
}

// ---------------------------------------------------------------------------
//                               数据面（拉流线程）
// ---------------------------------------------------------------------------

void ClipRecorder::on_packet(const Zhulong_packet_view &packet) noexcept {
    try {
        on_packet_body(packet);
    } catch (...) {
        // 典型是深拷贝 bad_alloc：截断活跃片段、清空缓冲，等下一个关键帧重建。
        ZL_LOGW(stream_id_, log_module, "clip_buffer_exception truncated=1");
        try {
            std::lock_guard<std::mutex> lock(mutex_);
            if (active_) {
                ++truncations_;
                end_active_locked(ZHULONG_CLIP_TRUNCATED);
            }
            clear_buffer_locked();
        } catch (...) {
            // end_active_locked 的入队同样可能 OOM：此时片段由 shutdown 收尾
        }
    }
}

void ClipRecorder::on_packet_body(const Zhulong_packet_view &packet) {
    if (packet.track_type != ZHULONG_TRACK_VIDEO || packet.data == nullptr || packet.size == 0) return;
    const bool key = packet.key_frame != 0;

    // 深拷贝在锁外：trigger 只需短暂持锁，不与一次 memcpy 争用
    auto sample = std::make_shared<ClipSample>();
    sample->pts_us = packet.pts_us;
    sample->dts_us = packet.dts_us;
    sample->key_frame = key;
    sample->bytes.assign(packet.data, packet.data + packet.size);
    const int64_t size = static_cast<int64_t>(packet.size);
    std::vector<uint8_t> key_param_sets;
    if (key) {
        key_param_sets = extract_annex_b_parameter_sets(packet.codec, packet.data, packet.size);
    }

    std::lock_guard<std::mutex> lock(mutex_);
    if (shut_) return;

    const bool flagged = (packet.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0;
    const bool gop_jump = has_last_gop_ && !key && packet.gop_id != last_gop_id_;
    const bool codec_changed = codec_ != 0 && packet.codec != codec_;
    const bool param_sets_changed =
        key && !key_param_sets.empty() && !last_parameter_sets_.empty() && key_param_sets != last_parameter_sets_;
    has_last_gop_ = true;
    last_gop_id_ = packet.gop_id;
    if ((flagged || gop_jump || codec_changed || param_sets_changed) && (!gops_.empty() || active_)) {
        ZL_LOGI(stream_id_, log_module,
                "clip_buffer_discontinuity flagged=%d gop_jump=%d codec_changed=%d param_changed=%d active=%d",
                flagged ? 1 : 0, gop_jump ? 1 : 0, codec_changed ? 1 : 0, param_sets_changed ? 1 : 0,
                active_ ? 1 : 0);
        if (active_) {
            ++truncations_;
            end_active_locked(ZHULONG_CLIP_TRUNCATED);
        }
        clear_buffer_locked();
        recent_backfill_.reset();
        has_bframes_ = false;
    }

    if (waiting_keyframe_) {
        if (!key) return;
        waiting_keyframe_ = false;
    }

    codec_ = packet.codec;
    if (key && !key_param_sets.empty()) {
        last_parameter_sets_ = std::move(key_param_sets);
    }
    if (packet.pts_us < packet.dts_us) {
        has_bframes_ = true;
    }
    if (gops_.empty() || key) {
        gops_.emplace_back();
        gops_.back().start_pts_us = packet.pts_us;
        newest_pts_us_ = std::max(newest_pts_us_, packet.pts_us);
    }
    gops_.back().samples.push_back(sample);
    gops_.back().bytes += size;
    buffered_bytes_ += size;
    newest_pts_us_ = std::max(newest_pts_us_, packet.pts_us);

    if (active_) feed_active_locked(sample);
    evict_locked();
}

void ClipRecorder::evict_locked() {
    if (gops_.empty()) return;
    const int64_t pre_us = ms_to_us_saturating(saturating_add(settings_.pre_ms, clip_buffer_event_lag_slack_ms));
    const int64_t cutoff_us = saturating_sub(newest_pts_us_, pre_us);
    // 保留覆盖 pre_ms（+ 事件延迟余量）所需的最少 GOP：第二个 GOP 已能覆盖时，最老的那个就多余了
    while (gops_.size() > 1 && gops_[1].start_pts_us <= cutoff_us) {
        buffered_bytes_ -= gops_.front().bytes;
        gops_.pop_front();
    }
    // 字节硬上限：整组淘汰，保证缓冲首包永远是关键帧
    while (gops_.size() > 1 && buffered_bytes_ > settings_.byte_cap) {
        buffered_bytes_ -= gops_.front().bytes;
        gops_.pop_front();
    }
    // 单个 GOP 已超过上限：放弃整组，等下一个关键帧（内存上限优先于前段完整性）。
    // 若此时有活跃片段，必须立即按 truncated 收尾，避免因 waiting_keyframe_ 导致片段中间黑洞。
    if (buffered_bytes_ > settings_.byte_cap) {
        ZL_LOGW(stream_id_, log_module, "clip_buffer_gop_oversize bytes=%lld cap=%lld",
                static_cast<long long>(buffered_bytes_), static_cast<long long>(settings_.byte_cap));
        if (active_) {
            ++truncations_;
            end_active_locked(ZHULONG_CLIP_TRUNCATED);
        }
        clear_buffer_locked();
    }
}

void ClipRecorder::clear_buffer_locked() noexcept {
    gops_.clear();
    buffered_bytes_ = 0;
    waiting_keyframe_ = true;
    // newest_pts_us_ 不回退：整形后的时间轴跨不连续也保持单调
}

void ClipRecorder::feed_active_locked(const ClipSamplePtr &sample) {
    if (sample->pts_us >= active_->end_pts_us) {
        end_active_locked(ZHULONG_CLIP_COMPLETED);
        return;
    }
    if (!try_enqueue_sample(sample)) {
        ++truncations_;
        ZL_LOGW(stream_id_, log_module, "clip_queue_overflow seq=%llu truncated=1",
                static_cast<unsigned long long>(active_->seq));
        end_active_locked(ZHULONG_CLIP_TRUNCATED);
    }
}

void ClipRecorder::end_active_locked(int32_t status) {
    WriterItem item;
    item.kind = WriterItemKind::end;
    item.seq = active_->seq;
    item.status = status;
    active_.reset();
    enqueue_forced(std::move(item));
}

void ClipRecorder::enqueue_forced(WriterItem &&item) {
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        if (item.kind == WriterItemKind::sample && item.sample) {
            queued_bytes_ += static_cast<int64_t>(item.sample->bytes.size());
            ++queued_samples_;
        }
        queue_.push_back(std::move(item));
    }
    queue_ready_.notify_one();
}

bool ClipRecorder::try_enqueue_sample(const ClipSamplePtr &sample) {
    const int64_t size = static_cast<int64_t>(sample->bytes.size());
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        if (queued_samples_ >= default_clip_queue_packet_cap || queued_bytes_ + size > queue_byte_cap_) {
            return false;
        }
        WriterItem item;
        item.kind = WriterItemKind::sample;
        item.sample = sample;
        queue_.push_back(std::move(item));
        queued_bytes_ += size;
        ++queued_samples_;
    }
    queue_ready_.notify_one();
    return true;
}

// ---------------------------------------------------------------------------
//                               控制面（ABI 线程）
// ---------------------------------------------------------------------------

ClipTriggerOutcome ClipRecorder::trigger(const ClipTriggerParams &params) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (shut_ || gops_.empty()) throw Failure{Zhulong_ERR_NOT_READY};

    const int64_t event_us = params.event_pts_us > 0 ? params.event_pts_us : newest_pts_us_;
    const int64_t post_us = ms_to_us_saturating(std::max<int64_t>(params.post_ms, 0));
    const int64_t max_us = ms_to_us_saturating(positive_or(params.max_ms, default_clip_max_ms));
    const int64_t wanted_end_us = saturating_add(event_us, post_us);

    if (active_) {
        if (wanted_end_us <= saturating_add(active_->start_pts_us, max_us)) {
            active_->end_pts_us = std::max(active_->end_pts_us, wanted_end_us);
            ClipTriggerOutcome extended{};
            extended.seq = active_->seq;
            extended.created = false;
            extended.codec = active_->codec;
            extended.offset_ms = std::max<int64_t>(0, (event_us - active_->start_pts_us) / 1000);
            extended.start_pts_us = active_->start_pts_us;
            return extended;
        }
        // 超长：立即结束旧片段（不再延长），新片段从缓冲中的关键帧开始，覆盖旧片段尾部
        end_active_locked(ZHULONG_CLIP_COMPLETED);
    } else if (recent_backfill_ && recent_backfill_->end_pts_us >= gops_.front().start_pts_us &&
               event_us >= recent_backfill_->start_pts_us &&
               wanted_end_us <= recent_backfill_->end_pts_us &&
               wanted_end_us <= saturating_add(recent_backfill_->start_pts_us, max_us)) {
        // 同帧多目标在推理延迟 > post_ms 时会全部在缓冲内直接收尾；让后续事件合并到同一 seq。
        ClipTriggerOutcome merged{};
        merged.seq = recent_backfill_->seq;
        merged.created = false;
        merged.codec = recent_backfill_->codec;
        merged.offset_ms = std::max<int64_t>(0, (event_us - recent_backfill_->start_pts_us) / 1000);
        merged.start_pts_us = recent_backfill_->start_pts_us;
        return merged;
    }

    // 写线程已积压满载（慢盘/卡死）时拒绝新建片段，避免反复回填 32 MiB 造成队列 OOM。
    {
        std::lock_guard<std::mutex> queue_lock(queue_mutex_);
        if (queued_samples_ >= default_clip_queue_packet_cap || queued_bytes_ >= queue_byte_cap_) {
            throw Failure{Zhulong_ERR_BUSY};
        }
    }

    // 起点 GOP：最后一个起点 <= event - pre 的 GOP；缓冲不足时取最老 GOP（前段变短，不算错误）
    const int64_t target_us = saturating_sub(event_us, ms_to_us_saturating(settings_.pre_ms));
    size_t start_index = 0;
    for (size_t i = 0; i < gops_.size(); ++i) {
        if (gops_[i].start_pts_us <= target_us) {
            start_index = i;
        } else {
            break;
        }
    }

    const int64_t start_pts_us = gops_[start_index].start_pts_us;
    const int64_t capped_end_us = std::min(wanted_end_us, saturating_add(start_pts_us, max_us));
    if (capped_end_us <= start_pts_us) {
        // 事件窗已完全滑出当前缓冲（无任何样本落在 [start, end) 内），拒绝建空片段
        throw Failure{Zhulong_ERR_NOT_READY};
    }

    auto request = std::make_shared<ClipWriteRequest>();
    request->path = params.path;
    request->codec = codec_;
    request->width_hint = params.width_hint;
    request->height_hint = params.height_hint;
    request->parameter_sets_hint = params.parameter_sets_hint;
    request->pts_reorder_offset_us = has_bframes_ ? default_bframe_pts_reorder_offset_us : 0;

    ActiveClip clip{};
    clip.seq = events_->next_sequence();
    clip.codec = codec_;
    clip.start_pts_us = start_pts_us;
    clip.end_pts_us = capped_end_us;

    // 回填条目先完整构建成批次再一次性入队：中途 OOM 不会留下「有 begin 无 end」的半片段
    std::deque<WriterItem> batch;
    {
        WriterItem begin;
        begin.kind = WriterItemKind::begin;
        begin.seq = clip.seq;
        begin.request = std::move(request);
        batch.push_back(std::move(begin));
    }
    int64_t batch_bytes = 0;
    size_t batch_samples = 0;
    bool reached_end = false;
    for (size_t i = start_index; i < gops_.size() && !reached_end; ++i) {
        for (const auto &sample : gops_[i].samples) {
            if (sample->pts_us >= clip.end_pts_us) {
                reached_end = true;
                break;
            }
            WriterItem item;
            item.kind = WriterItemKind::sample;
            item.sample = sample;
            batch.push_back(std::move(item));
            batch_bytes += static_cast<int64_t>(sample->bytes.size());
            ++batch_samples;
        }
    }
    if (reached_end) {
        WriterItem end;
        end.kind = WriterItemKind::end;
        end.seq = clip.seq;
        end.status = ZHULONG_CLIP_COMPLETED;
        batch.push_back(std::move(end));
    }
    {
        std::lock_guard<std::mutex> queue_lock(queue_mutex_);
        for (auto &item : batch) queue_.push_back(std::move(item));
        queued_bytes_ += batch_bytes;
        queued_samples_ += batch_samples;
    }
    queue_ready_.notify_one();
    if (reached_end) {
        recent_backfill_ = clip;
    } else {
        recent_backfill_.reset();
        active_ = clip;
    }

    ClipTriggerOutcome created{};
    created.seq = clip.seq;
    created.created = true;
    created.codec = clip.codec;
    created.offset_ms = std::max<int64_t>(0, (event_us - clip.start_pts_us) / 1000);
    created.start_pts_us = clip.start_pts_us;
    ZL_LOGI(stream_id_, log_module, "clip_started seq=%llu codec=%d offset_ms=%lld backlog=%zu",
            static_cast<unsigned long long>(clip.seq), clip.codec,
            static_cast<long long>(created.offset_ms), batch_samples);
    return created;
}

void ClipRecorder::shutdown(std::chrono::milliseconds budget) noexcept {
    std::lock_guard<std::mutex> guard(shutdown_mutex_);
    if (shutdown_done_) return;
    shutdown_done_ = true;

    {
        std::lock_guard<std::mutex> lock(mutex_);
        shut_ = true;
        if (active_) {
            try {
                end_active_locked(ZHULONG_CLIP_COMPLETED);
            } catch (...) {
                // 入队失败：写线程退出时会把未收尾的片段按 truncated 处理
                active_.reset();
            }
        }
        recent_backfill_.reset();
        clear_buffer_locked();
    }
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        stop_requested_ = true;
    }
    queue_ready_.notify_all();

    {
        std::unique_lock<std::mutex> lock(queue_mutex_);
        const bool exited = writer_exited_cv_.wait_for(lock, budget, [this] { return writer_exited_; });
        if (!exited) {
            abort_.store(true);
            ZL_LOGW(stream_id_, log_module, "clip_shutdown_budget_exceeded aborting=1");
        }
    }
    queue_ready_.notify_all();
    if (writer_.joinable()) {
        try {
            writer_.join();
        } catch (...) {
            ZL_LOGE(stream_id_, log_module, "clip_writer_join_exception");
        }
    }
}

// ---------------------------------------------------------------------------
//                               写线程
// ---------------------------------------------------------------------------

void ClipRecorder::writer_loop() noexcept {
    for (;;) {
        WriterItem item;
        {
            std::unique_lock<std::mutex> lock(queue_mutex_);
            queue_ready_.wait(lock, [this] { return !queue_.empty() || stop_requested_; });
            if (queue_.empty()) break; // stop_requested_ 且已排空
            item = std::move(queue_.front());
            queue_.pop_front();
            if (item.kind == WriterItemKind::sample && item.sample) {
                queued_bytes_ -= static_cast<int64_t>(item.sample->bytes.size());
                --queued_samples_;
            }
        }
        process_item(item);
    }
    // 理论上 shutdown 已入队 end；入队失败的兜底：未收尾片段按 truncated 结束
    if (writing_) finalize_writing(ZHULONG_CLIP_TRUNCATED);
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        writer_exited_ = true;
    }
    writer_exited_cv_.notify_all();
}

void ClipRecorder::process_item(WriterItem &item) noexcept {
    try {
        switch (item.kind) {
        case WriterItemKind::begin: {
            if (writing_) finalize_writing(ZHULONG_CLIP_TRUNCATED);
            writing_.emplace();
            writing_->seq = item.seq;
            writing_->codec = item.request ? item.request->codec : 0;
            if (item.request) {
                writing_->writer = writer_factory_(*item.request);
            }
            writing_->failed = !writing_->writer;
            break;
        }
        case WriterItemKind::sample: {
            if (!writing_ || !item.sample) break;
            if (abort_.load(std::memory_order_relaxed)) {
                writing_->aborted = true;
                break;
            }
            if (writing_->failed) break;
            const ClipSample &sample = *item.sample;
            if (!writing_->writer->write(sample)) {
                writing_->failed = true;
                break;
            }
            if (writing_->samples == 0) {
                writing_->first_pts_us = sample.pts_us;
                writing_->first_dts_us = sample.dts_us;
                writing_->max_pts_us = sample.pts_us;
            }
            ++writing_->samples;
            writing_->last_pts_us = sample.pts_us;
            writing_->last_dts_us = sample.dts_us;
            writing_->max_pts_us = std::max(writing_->max_pts_us, sample.pts_us);
            break;
        }
        case WriterItemKind::end: {
            if (writing_ && writing_->seq == item.seq) finalize_writing(item.status);
            break;
        }
        }
    } catch (...) {
        ZL_LOGE(stream_id_, log_module, "clip_writer_exception");
        if (writing_) writing_->failed = true;
    }
}

void ClipRecorder::finalize_writing(int32_t status) noexcept {
    WritingClip &clip = *writing_;
    ClipEvent event{};
    event.seq = clip.seq;
    event.codec = clip.codec;
    event.status = status;
    if (clip.aborted && status == ZHULONG_CLIP_COMPLETED) event.status = ZHULONG_CLIP_TRUNCATED;

    if (!clip.writer || clip.failed || clip.samples == 0) {
        if (clip.writer) clip.writer->abort();
        event.status = ZHULONG_CLIP_FAILED;
    } else {
        int64_t size = 0;
        if (clip.writer->finish(&size)) {
            event.size_bytes = size;
        } else {
            event.status = ZHULONG_CLIP_FAILED;
        }
    }
    if (clip.samples > 0) {
        event.first_pts_us = clip.first_pts_us;
        event.last_pts_us = clip.max_pts_us;
        // 时长 = 首末 dts 跨度 + 一个平均帧间隔（最后一帧本身也占时长）
        const int64_t span_us = clip.last_dts_us - clip.first_dts_us;
        const int64_t frame_us = clip.samples > 1 ? span_us / (clip.samples - 1) : 0;
        event.duration_ms = (span_us + frame_us) / 1000;
    }
    ZL_LOGI(stream_id_, log_module, "clip_finished seq=%llu status=%d samples=%lld duration_ms=%lld size=%lld",
            static_cast<unsigned long long>(event.seq), event.status, static_cast<long long>(clip.samples),
            static_cast<long long>(event.duration_ms), static_cast<long long>(event.size_bytes));
    writing_.reset();
    events_->push(event);
}

} // namespace zhulong
