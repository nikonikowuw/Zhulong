/**
 * @file ai_subscription.hpp
 * @brief AI 推理结果事件订阅上下文与同步排空状态机
 */

#pragma once

#include "Zhulong/engine.h"
#include "pipeline/subscription.hpp"

#include <cstddef>
#include <cstdint>

namespace zhulong {

/**
 * @brief AI 推理结果订阅通道上下文
 */
struct AISubscription : public SubscriptionBase {
    Zhulong_stream_id stream_id{0};             /**< 关联的物理流 ID */
    Zhulong_ai_result_callback callback{nullptr};/**< 结果回调函数指针 */
    uintptr_t token{0};                         /**< 用户上下文令牌 */

    AISubscription(Zhulong_stream_id s_id, Zhulong_ai_result_callback cb, uintptr_t tok)
        : stream_id(s_id), callback(cb), token(tok) {}

    void invoke(Zhulong_stream_id s_id, const char* json_payload, size_t payload_len) {
        if (!callback) return;
        execute_callback([&] {
            callback(token, s_id, json_payload, payload_len);
        });
    }
};

} // namespace zhulong
