/**
 * @file bounded_queue.hpp
 * @brief 具备背压控制、溢出淘汰策略与停机协作式取消的通用有界队列
 *
 * 核心设计原则：
 * 1. 背压与溢出策略（Backpressure & Overflow Strategies）：
 *    - BLOCK：写者阻塞等待空位，保证零丢帧（适用于关键帧或录像流）；
 *    - DROP_OLDEST：丢弃队列头部最旧元素，保证消费者始终处理最新画面（适用于实时 AI 推理帧队列）；
 *    - DROP_NEWEST：队列满载时直接拒绝新入队元素（适用于解码数据包拥塞控制）。
 * 2. 锁外析构（Lock-free Destruction of Discarded Elements）：
 *    - 淘汰或清空的元素先移出至局部变量（discarded），在释放互斥锁后才执行析构，
 *      防止持有队列锁期间触发复杂的析构逻辑（如 HardwareFrame 的 release_fn 闭包）导致潜在死锁。
 * 3. 协作式不可逆取消（Non-reentrant Cancellation）：
 *    - cancel() 置位 cancelled_ 标记并广播唤醒所有等待的读者与写者；
 *    - ⚠️ 一旦取消则不可复用，后续所有 push / pop 操作立即返回 false；
 *    - 上层流水线若需恢复流处理，必须重建新的会话对象（如 DecodeSession 重建），不得复用已取消的队列。
 */

#pragma once

#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <deque>
#include <mutex>
#include <utility>

namespace zhulong {

/** @brief 队列溢出策略枚举 */
enum class OverflowStrategy {
    BLOCK = 0,       /**< 阻塞等待队列空位 (适用于关键帧与高可靠分发) */
    DROP_OLDEST = 1, /**< 丢弃队列头部最旧数据 (适用于实时 AI 帧队列，确保低延迟消费最新画面) */
    DROP_NEWEST = 2  /**< 丢弃当前新数据 (队列满时拒绝新入队，由上层触发拥塞重同步) */
};

/**
 * @brief 线程安全泛型有界队列模板类
 * @tparam T 队列承载的元素类型
 */
template <typename T>
class BoundedQueue {
public:
    /**
     * @brief 构造函数
     * @param capacity 队列容量上限 (必须大于 0)
     * @param strategy 满载时的溢出淘汰策略
     */
    explicit BoundedQueue(size_t capacity, OverflowStrategy strategy = OverflowStrategy::BLOCK)
        : capacity_(capacity > 0 ? capacity : 1), strategy_(strategy) {}

    /** @brief 析构函数，自动触发取消并唤醒所有等待线程 */
    ~BoundedQueue() {
        cancel();
    }

    /**
     * @brief 数据入队
     *
     * ⚠️ 锁外析构：
     *   - 在 DROP_OLDEST 策略下被淘汰的最旧元素，在锁外完成移动与析构，防止持有队列锁时触发复杂的外部回收。
     *
     * @param item 待入队的数据元素
     * @return true 成功入队；false 队列已被取消或因 DROP_NEWEST 策略被拒绝
     */
    bool push(T item) {
        T discarded;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            if (cancelled_) return false;

            if (queue_.size() >= capacity_) {
                if (strategy_ == OverflowStrategy::DROP_OLDEST) {
                    // 移出最旧元素并在锁外析构，避免持锁执行潜在复杂的回收闭包
                    discarded = std::move(queue_.front());
                    queue_.pop_front();
                } else if (strategy_ == OverflowStrategy::DROP_NEWEST) {
                    return false;
                } else { // BLOCK
                    not_full_.wait(lock, [this] {
                        return cancelled_ || queue_.size() < capacity_;
                    });
                    if (cancelled_) return false;
                }
            }

            queue_.push_back(std::move(item));
            not_empty_.notify_one();
        }
        return true;
    }

    /**
     * @brief 带超时的数据出队
     * @tparam Rep 时钟周期表示类型
     * @tparam Period 时钟周期比例类型
     * @param[out] item 成功时接收出队数据的引用
     * @param timeout 最大等待超时时长
     * @return true 成功获取数据；false 等待超时或队列已被取消
     */
    template <typename Rep, typename Period>
    bool pop(T &item, const std::chrono::duration<Rep, Period> &timeout) {
        std::unique_lock<std::mutex> lock(mutex_);
        if (!not_empty_.wait_for(lock, timeout, [this] {
            return cancelled_ || !queue_.empty();
        })) {
            return false; // 等待超时
        }

        if (queue_.empty()) {
            return false; // 队列已被取消且已无残余数据
        }

        item = std::move(queue_.front());
        queue_.pop_front();
        not_full_.notify_one();
        return true;
    }

    /**
     * @brief 阻塞直到出队成功或队列被取消
     * @param[out] item 成功时接收出队数据的引用
     * @return true 成功获取数据；false 队列已被取消且为空
     */
    bool pop(T &item) {
        std::unique_lock<std::mutex> lock(mutex_);
        not_empty_.wait(lock, [this] {
            return cancelled_ || !queue_.empty();
        });

        if (queue_.empty()) {
            return false;
        }

        item = std::move(queue_.front());
        queue_.pop_front();
        not_full_.notify_one();
        return true;
    }

    /**
     * @brief 协作式取消队列，唤醒所有阻塞等待的生产者与消费者
     *
     * ⚠️ 不可逆性契约：
     *   - 一旦取消，cancelled_ 恒为 true，队列不再接受新数据且无法复活；
     *   - 残余数据在锁外释放，清空内存。
     */
    void cancel() {
        std::deque<T> discarded;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            cancelled_ = true;
            discarded = std::move(queue_);
        }
        not_empty_.notify_all();
        not_full_.notify_all();
    }

    /**
     * @brief 仅清空残余元素，不改变取消标记
     *
     * 适用于拥塞重同步场景（如跳过非关键帧包），残余数据在锁外析构。
     * 即使在停机竞态下调用也不会意外复活已取消的队列。
     */
    void clear() {
        std::deque<T> discarded;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            discarded = std::move(queue_);
            queue_.clear();
        }
        not_full_.notify_all();
    }

    /** @brief 检查队列是否已被取消 */
    bool is_cancelled() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return cancelled_;
    }

    /** @brief 获取当前队列中积压的元素个数 */
    size_t size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return queue_.size();
    }

    /** @brief 检查队列是否为空 */
    bool empty() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return queue_.empty();
    }

    /** @brief 获取队列的最大容量配置 */
    size_t capacity() const {
        return capacity_;
    }

private:
    const size_t capacity_;                 /**< 队列容量上限 */
    const OverflowStrategy strategy_;       /**< 溢出淘汰策略 */
    mutable std::mutex mutex_;              /**< 保护内部 deque 与状态 */
    std::condition_variable not_empty_;     /**< 队列非空条件变量 */
    std::condition_variable not_full_;      /**< 队列非满条件变量 */
    std::deque<T> queue_;                   /**< 元素容器 */
    bool cancelled_{false};                 /**< 不可逆取消状态标志 */
};

} // namespace zhulong
