/**
 * @file subscription.cpp
 * @brief 数据包订阅通道实现
 */

#include "pipeline/subscription.hpp"

namespace zhulong {

thread_local bool in_callback = false;

void Subscription::invoke(const Zhulong_packet_view &packet) {
    // 快速加锁检查状态并标记 active
    {
        std::lock_guard<std::mutex> lock(mutex);
        if (!enabled) return;
        active = true;
    }

    // 设置线程局部标记，防止用户在回调中非法调用控制接口导致自死锁
    in_callback = true;
    try {
        // 在锁外调用用户注册的回调函数，避免用户代码耗时阻塞分发通道
        callback(token, &packet);
    } catch (...) {
        // 异常防御：即使异常的用户回调抛出了 C++ 异常，也必须恢复状态并唤醒等待排空的线程
        in_callback = false;
        std::lock_guard<std::mutex> lock(mutex);
        active = false;
        enabled = false;
        drained.notify_all();
        throw;
    }

    // 正常退出：清理回调标记并通知排空条件变量
    in_callback = false;
    std::lock_guard<std::mutex> lock(mutex);
    active = false;
    drained.notify_all();
}

void Subscription::disable_and_drain() {
    std::unique_lock<std::mutex> lock(mutex);
    // 禁用后续派发
    enabled = false;
    // 阻塞等待当前正在执行的回调完全返回
    drained.wait(lock, [this] { return !active; });
}

} // namespace zhulong
