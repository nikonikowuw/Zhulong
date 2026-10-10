/**
 * @file subscription.hpp
 * @brief 数据包订阅通道上下文与同步排空状态机
 */

#pragma once

#include "Zhulong/engine.h"

#include <condition_variable>
#include <cstdint>
#include <mutex>

namespace zhulong {

/**
 * @brief 线程局部变量：标记当前线程是否正处于用户包回调调用链中
 * 用于 C ABI 边界防自死锁与非法重入检测。
 */
extern thread_local bool in_callback;

/**
 * @brief 数据包订阅通道上下文
 *
 * 维护用户注册的回调函数与状态，支持在单生产者模型下的安全调用与同步排空。
 */
struct Subscription {
    uint64_t consumer;                          /**< 关联的预览消费者 ID */
    Zhulong_packet_callback callback;           /**< 用户提供的包回调函数指针 */
    uintptr_t token;                            /**< 用户上下文令牌（不可保留为裸 Go 指针） */
    std::mutex mutex;                           /**< 保护 enabled 与 active 状态 */
    std::condition_variable drained;            /**< 当 active 变为 false 时发出的排空信号 */
    bool enabled = true;                        /**< 订阅是否有效，置 false 即拒绝后续派发 */
    bool active = false;                        /**< 当前是否有回调正在执行中（单流单生产者） */

    Subscription(uint64_t consumer_id, Zhulong_packet_callback function, uintptr_t value)
        : consumer(consumer_id), callback(function), token(value) {}

    /**
     * @brief 派发数据包给回调函数（在锁外调用，具备异常安全防护）
     * @param packet 数据包视图借用指针
     */
    void invoke(const Zhulong_packet_view &packet);

    /**
     * @brief 禁用该订阅并阻塞等待所有正在执行的回调彻底排空
     */
    void disable_and_drain();
};

} // namespace zhulong
