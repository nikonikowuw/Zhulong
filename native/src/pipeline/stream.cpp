/**
 * @file stream.cpp
 * @brief 物理流上下文与节点处理流水线实现
 */

#include "pipeline/stream.hpp"

#include <chrono>
#include <utility>

namespace zhulong {

Stream::Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options)
    : id(stream_id), url(std::move(normalized_url)), options(stream_options) {}

Stream::~Stream() {
    request_stop();
    join();
}

void Stream::start() {
    // 检查并按需初始化解码节点
    sync_decode_pipeline();
    // 启动拉流 Worker 工作线程
    worker = std::thread(&Stream::capture, this);
}

void Stream::request_stop() {
    // 设置协作式取消标志，中断底层网络 IO 与循环
    cancel.stopped.store(true);
    decode_active.store(false);
    packet_queue.cancel();
}

void Stream::join() {
    if (decode_worker.joinable()) {
        decode_worker.join();
    }
    if (worker.joinable()) {
        worker.join();
    }
    frame_queue.cancel();
}

bool Stream::has_ai_consumer() const {
    for (const auto &entry : consumers) {
        if (entry.second == Zhulong_CONSUMER_AI) {
            return true;
        }
    }
    return false;
}

void Stream::sync_decode_pipeline() {
    std::lock_guard<std::mutex> lock(mutex);
    bool should_decode = has_ai_consumer();

    if (should_decode && !decode_active.load()) {
        decode_active.store(true);
        decode_worker = std::thread(&Stream::decode_loop, this);
    } else if (!should_decode && decode_active.load()) {
        decode_active.store(false);
        packet_queue.cancel();
        if (decode_worker.joinable()) {
            decode_worker.join();
        }
        frame_queue.cancel();
    }
}

void Stream::dispatch(const Zhulong_packet_view &packet) {
    // 1. 快照机制：在锁保护下复制当前的订阅者指针列表，在锁外分发
    std::vector<std::shared_ptr<Subscription>> snapshot;
    {
        std::lock_guard<std::mutex> lock(mutex);
        for (const auto &entry : subscriptions) {
            snapshot.push_back(entry.second);
        }
    }

    for (const auto &subscription : snapshot) {
        if (cancel.stopped.load()) break;
        subscription->invoke(packet);
    }

    // 2. 若存在活跃的解码流程，将数据包放入解码有界队列 (Drop-Oldest 防背压堆积)
    if (decode_active.load() && !cancel.stopped.load() && packet.data && packet.size > 0) {
        QueuedPacket qp;
        qp.data.assign(packet.data, packet.data + packet.size);
        qp.pts = packet.pts;
        qp.dts = packet.dts;
        qp.codec = packet.codec;
        qp.key_frame = packet.key_frame;
        packet_queue.push(std::move(qp));
    }
}

void Stream::decode_loop() noexcept {
    std::unique_ptr<IDecodeNode> decoder;
    while (decode_active.load() && !cancel.stopped.load()) {
        QueuedPacket pkt;
        if (!packet_queue.pop(pkt, std::chrono::milliseconds(50))) {
            continue;
        }

        if (cancel.stopped.load() || !decode_active.load()) break;

        // 懒初始化解码器实例
        if (!decoder) {
            try {
                decoder = IDecodeNode::create(pkt.codec);
            } catch (...) {
                continue;
            }
        }

        if (decoder) {
            int ret = decoder->send_packet(pkt.data.data(), pkt.data.size(), pkt.pts, pkt.dts);
            if (ret >= 0) {
                HardwareFrame::Ptr frame;
                while (decoder->receive_frame(frame) == 0 && frame != nullptr) {
                    frame->stream_id = id;
                    // 送入实时 AI 帧队列 (容量为 3，若满自动 Drop-Oldest 丢弃最旧帧)
                    frame_queue.push(frame);
                }
            }
        }
    }
}

void Stream::capture() noexcept {
    Zhulong_status_t error = Zhulong_OK;
    try {
        read_rtsp(url, options, cancel,
            [this](const Zhulong_packet_view &packet) {
                dispatch(packet);
            },
            [this] {
                std::lock_guard<std::mutex> lock(mutex);
                status.state = Zhulong_STREAM_RUNNING;
            });
    } catch (const Failure &failure) {
        error = failure.status;
    } catch (const std::bad_alloc &) {
        error = Zhulong_ERR_OUT_OF_MEMORY;
    } catch (...) {
        error = Zhulong_ERR_INTERNAL;
    }

    std::lock_guard<std::mutex> lock(mutex);
    status = {Zhulong_STREAM_FAILED, error == Zhulong_OK ? Zhulong_ERR_CANCELLED : error};
}

} // namespace zhulong
