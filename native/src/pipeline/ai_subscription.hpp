/**
 * @file ai_subscription.hpp
 * @brief AI 推理结果事件订阅上下文与同步排空状态机
 */

#pragma once

#include "Zhulong/engine.h"

#include <condition_variable>
#include <cstdint>
#include <mutex>

namespace zhulong {

/**
 * @brief AI 推理结果订阅通道上下文
 */
struct AISubscription {
    Zhulong_stream_id stream_id{0};             /**< 关联的物理流 ID */
    Zhulong_ai_result_callback callback{nullptr};/**< 结果回调函数指针 */
    uintptr_t token{0};                         /**< 用户上下文令牌 */
    std::mutex mutex;                           /**< 保护 enabled 与 active 状态 */
    std::condition_variable drained;            /**< 当 active 变为 false 时发出的排空信号 */
    bool enabled{true};                         /**< 是否有效 */
    bool active{false};                         /**< 当前是否有回调正在执行 */

    AISubscription(Zhulong_stream_id s_id, Zhulong_ai_result_callback cb, uintptr_t tok)
        : stream_id(s_id), callback(cb), token(tok) {}

    void invoke(Zhulong_stream_id s_id, const char* json_payload, size_t payload_len) {
        std::unique_lock<std::mutex> lock(mutex);
        if (!enabled || !callback) return;
        active = true;
        lock.unlock();

        try {
            callback(token, s_id, json_payload, payload_len);
        } catch (...) {}

        lock.lock();
        active = false;
        drained.notify_all();
    }

    void disable_and_drain() {
        std::unique_lock<std::mutex> lock(mutex);
        enabled = false;
        drained.wait(lock, [this]() { return !active; });
    }
};

} // namespace zhulong
