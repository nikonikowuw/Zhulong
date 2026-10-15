/**
 * @file subscription.hpp
 * @brief 数据包订阅通道上下文与同步排空状态机
 *
 * 核心机制：
 * 1. 同步排空（Drain）：
 *    - 当外部调用 unsubscribe 时，先置 enabled=false 阻止新分发，再阻塞等待当前正在执行的回调彻底退出（active=false）；
 *    - 确保返回后外部可安全销毁令牌（如 cgo.Handle.Delete()），杜绝回调向已注销的目标发送数据。
 * 2. 线程局部重入防御（Re-entrancy Guard）：
 *    - 通过 in_callback 标志拦截在数据包分发上下文中对引擎控制 API 的重入调用，防止自死锁。
 */

#pragma once

#include "Zhulong/engine.h"

#include <condition_variable>
#include <cstdint>
#include <mutex>

namespace zhulong {

/**
 * @brief 线程局部变量：标记当前线程是否正处于用户包回调调用链中
 *
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
    uintptr_t token;                            /**< 用户上下文令牌（Go 侧为 cgo.Handle 整数，不可保留裸 Go 指针） */
    std::mutex mutex;                           /**< 保护 enabled 与 active 状态 */
    std::condition_variable drained;            /**< 当 active 变为 false 时发出的排空信号 */
    bool enabled = true;                        /**< 订阅是否有效，置 false 即拒绝后续派发 */
    bool active = false;                        /**< 当前是否有回调正在执行中（单流单生产者） */

    /**
     * @brief 构造函数
     * @param consumer_id 消费者唯一标识
     * @param function 用户回调函数指针
     * @param value 上下文令牌
     */
    Subscription(uint64_t consumer_id, Zhulong_packet_callback function, uintptr_t value)
        : consumer(consumer_id), callback(function), token(value) {}

    /**
     * @brief 派发数据包给回调函数（在锁外调用，具备异常安全防护）
     * @param packet 数据包视图借用指针
     */
    void invoke(const Zhulong_packet_view &packet);

    /**
     * @brief 禁用该订阅并阻塞等待所有正在执行的回调彻底排空
     *
     * 同步阻塞直至 active 变为 false。返回后保证没有任何回调在运行，且后续也不会再触发。
     */
    void disable_and_drain();
};

} // namespace zhulong
