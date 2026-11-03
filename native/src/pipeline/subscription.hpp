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
 * @brief 回调重入防御 RAII 守卫：在生命周期内置位 in_callback，析构时自动恢复此前状态
 */
class InCallbackGuard {
public:
    InCallbackGuard() noexcept;
    ~InCallbackGuard() noexcept;
    InCallbackGuard(const InCallbackGuard&) = delete;
    InCallbackGuard& operator=(const InCallbackGuard&) = delete;

private:
    bool previous_{false};
};

/**
 * @brief 订阅通道同步排空基类
 *
 * 封装 enabled/active 状态、互斥锁、条件变量与同步排空状态机。
 */
class SubscriptionBase {
public:
    std::mutex mutex;                           /**< 保护 enabled 与 active 状态 */
    std::condition_variable drained;            /**< 当 active 变为 false 时发出的排空信号 */
    bool enabled{true};                         /**< 订阅是否有效，置 false 即拒绝后续派发 */
    bool active{false};                         /**< 当前是否有回调正在执行中 */

    SubscriptionBase() = default;
    virtual ~SubscriptionBase() = default;

    SubscriptionBase(const SubscriptionBase&) = delete;
    SubscriptionBase& operator=(const SubscriptionBase&) = delete;
    SubscriptionBase(SubscriptionBase&&) = delete;
    SubscriptionBase& operator=(SubscriptionBase&&) = delete;

    /**
     * @brief 禁用该订阅并阻塞等待所有正在执行的回调彻底排空
     *
     * 同步阻塞直至 active 变为 false。返回后保证没有任何回调在运行，且后续也不会再触发。
     */
    void disable_and_drain();

protected:
    /**
     * @brief 模板化回调执行包装器：处理快速持锁、锁外调用、TLS 防重入与异常时安全排空
     */
    template <typename Invoker>
    void execute_callback(Invoker&& invoker) {
        {
            std::lock_guard<std::mutex> lock(mutex);
            if (!enabled) return;
            active = true;
        }

        try {
            InCallbackGuard guard;
            invoker();
        } catch (...) {
            std::lock_guard<std::mutex> lock(mutex);
            active = false;
            enabled = false;
            drained.notify_all();
            throw;
        }

        std::lock_guard<std::mutex> lock(mutex);
        active = false;
        drained.notify_all();
    }
};

/**
 * @brief 数据包订阅通道上下文
 *
 * 维护用户注册的回调函数与状态，支持在单生产者模型下的安全调用与同步排空。
 */
struct Subscription : public SubscriptionBase {
    uint64_t consumer{0};                       /**< 关联的预览消费者 ID */
    Zhulong_packet_callback callback{nullptr};  /**< 用户提供的包回调函数指针 */
    uintptr_t token{0};                         /**< 用户上下文令牌（Go 侧为 cgo.Handle 整数，不可保留裸 Go 指针） */
    uint32_t track_mask{ZHULONG_TRACK_MASK_VIDEO}; /**< 订阅轨道掩码 (v5) */

    /**
     * @brief 构造函数
     * @param consumer_id 消费者唯一标识
     * @param function 用户回调函数指针
     * @param value 上下文令牌
     * @param tracks 订阅轨道掩码（默认为仅视频）
     */
    Subscription(uint64_t consumer_id, Zhulong_packet_callback function, uintptr_t value,
                 uint32_t tracks = ZHULONG_TRACK_MASK_VIDEO)
        : consumer(consumer_id), callback(function), token(value), track_mask(tracks) {}

    /**
     * @brief 派发数据包给回调函数（在锁外调用，具备异常安全防护）
     * @param packet 数据包视图借用指针
     */
    void invoke(const Zhulong_packet_view &packet);
};

} // namespace zhulong
