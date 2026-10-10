/**
 * @file bounded_queue.hpp
 * @brief 具备背压控制、溢出淘汰策略与停机协作式取消的通用有界队列
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
    BLOCK = 0,       /**< 阻塞等待队列空位 (适用于录像或关键帧) */
    DROP_OLDEST = 1, /**< 丢弃队列头部最旧数据 (适用于实时 AI 帧队列，保障最新) */
    DROP_NEWEST = 2  /**< 丢弃当前新数据 (队列满时拒绝新入队) */
};

template <typename T>
class BoundedQueue {
public:
    explicit BoundedQueue(size_t capacity, OverflowStrategy strategy = OverflowStrategy::BLOCK)
        : capacity_(capacity > 0 ? capacity : 1), strategy_(strategy) {}

    ~BoundedQueue() {
        cancel();
    }

    /**
     * @brief 数据入队
     * @param item 入队数据
     * @return true 成功入队; false 已被取消或被 DROP_NEWEST 丢弃
     */
    bool push(T item) {
        std::unique_lock<std::mutex> lock(mutex_);
        if (cancelled_) return false;

        if (queue_.size() >= capacity_) {
            if (strategy_ == OverflowStrategy::DROP_OLDEST) {
                // 弹出并销毁最旧元素 (若为 Frame::Ptr 将自动触发析构回收)
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
        return true;
    }

    /**
     * @brief 数据出队 (带超时)
     * @param[out] item 输出数据
     * @param timeout 最大等待超时
     * @return true 成功获取; false 超时或已被取消
     */
    template <typename Rep, typename Period>
    bool pop(T &item, const std::chrono::duration<Rep, Period> &timeout) {
        std::unique_lock<std::mutex> lock(mutex_);
        if (!not_empty_.wait_for(lock, timeout, [this] {
            return cancelled_ || !queue_.empty();
        })) {
            return false; // 超时
        }

        if (queue_.empty()) {
            return false; // 已取消且队列空
        }

        item = std::move(queue_.front());
        queue_.pop_front();
        not_full_.notify_one();
        return true;
    }

    /**
     * @brief 阻塞直到出队成功或队列被取消
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
     */
    void cancel() {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            cancelled_ = true;
            queue_.clear();
        }
        not_empty_.notify_all();
        not_full_.notify_all();
    }

    /** @brief 检查队列是否已被取消 */
    bool is_cancelled() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return cancelled_;
    }

    /** @brief 获取当前队列中元素个数 */
    size_t size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return queue_.size();
    }

    /** @brief 检查队列是否为空 */
    bool empty() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return queue_.empty();
    }

    /** @brief 获取队列最大容量 */
    size_t capacity() const {
        return capacity_;
    }

private:
    const size_t capacity_;
    const OverflowStrategy strategy_;
    mutable std::mutex mutex_;
    std::condition_variable not_empty_;
    std::condition_variable not_full_;
    std::deque<T> queue_;
    bool cancelled_{false};
};

} // namespace zhulong
