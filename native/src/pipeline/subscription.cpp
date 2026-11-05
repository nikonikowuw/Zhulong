/**
 * @file subscription.cpp
 * @brief 数据包订阅通道实现
 *
 * 核心实现说明：
 * 1. 锁外调用（Lock-free Callback Invocation）：
 *    - 快速持有 mutex 将 active 标记为 true 后立即释放锁，在锁外执行用户的 callback 函数，
 *      彻底杜绝用户回调耗时阻塞媒体分发循环；
 * 2. 异常隔离与排空保障（Exception-safe Drain Notification）：
 *    - 在 try-catch 块中保护回调调用；若用户回调抛出异常，无论如何均重置 active=false，
 *      并将 enabled 置为 false 防止故障蔓延，最后必定通过 notify_all() 唤醒等待排空的线程。
 */

#include "pipeline/subscription.hpp"

namespace zhulong {

thread_local bool in_callback = false;

InCallbackGuard::InCallbackGuard() noexcept : previous_(in_callback) {
    in_callback = true;
}

InCallbackGuard::~InCallbackGuard() noexcept {
    in_callback = previous_;
}

void Subscription::invoke(const Zhulong_packet_view &packet) {
    if (!callback) return;
    execute_callback([&] {
        // 在锁外调用用户注册的回调函数，避免用户代码耗时阻塞分发通道
        callback(token, &packet);
    });
}

void SubscriptionBase::disable_and_drain() {
    std::unique_lock<std::mutex> lock(mutex_);
    // 禁用后续派发
    enabled_ = false;
    // 阻塞等待当前正在执行的回调完全返回
    drained_.wait(lock, [this] { return active_count_ == 0; });
}

} // namespace zhulong
