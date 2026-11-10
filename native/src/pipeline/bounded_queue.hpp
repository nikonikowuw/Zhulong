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
#include <optional>
#include <utility>

namespace zhulong {

/** @brief 队列溢出策略枚举 */
enum class OverflowStrategy {
    block = 0,       /**< 阻塞等待队列空位 (适用于关键帧与高可靠分发) */
    drop_oldest = 1, /**< 丢弃队列头部最旧数据 (适用于实时 AI 帧队列，确保低延迟消费最新画面) */
    drop_newest = 2  /**< 丢弃当前新数据 (队列满时拒绝新入队，由上层触发拥塞重同步) */
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
    explicit BoundedQueue(size_t capacity, OverflowStrategy strategy = OverflowStrategy::block)
        : capacity_(capacity > 0 ? capacity : 1), strategy_(strategy) {}

    /**
     * @brief 析构函数，置位取消并唤醒所有等待线程
     *
     * ⚠️ 这里刻意不调用 cancel()：cancel() 为了锁外析构会构造临时 `std::deque`（libstdc++
     *    下默认构造即分配），而析构函数隐式 noexcept，OOM 时就是 std::terminate。
     *    析构时已不可能再有并发访问者，残余元素随 queue_ 成员析构即可（天然在锁外）。
     */
    ~BoundedQueue() {
        mark_cancelled();
    }

    BoundedQueue(const BoundedQueue&) = delete;
    BoundedQueue& operator=(const BoundedQueue&) = delete;
    BoundedQueue(BoundedQueue&&) = delete;
    BoundedQueue& operator=(BoundedQueue&&) = delete;

    /**
     * @brief 数据入队（左值引用）
     *
     * ⚠️ 锁外析构：
     *   - 在 DROP_OLDEST 策略下被淘汰的最旧元素，在锁外完成移动与析构，防止持有队列锁时触发复杂的外部回收。
     *
     * @param item 待入队的数据元素
     * @return true 成功入队；false 队列已被取消或因 DROP_NEWEST 策略被拒绝
     */
    bool push(const T &item) {
        return push_internal(item);
    }

    /**
     * @brief 数据入队（右值移动）
     *
     * ⚠️ 锁外析构：
     *   - 在 DROP_OLDEST 策略下被淘汰的最旧元素，在锁外完成移动与析构；
     *   - 被拒收的 item 仍在调用方作用域析构（锁外）。
     *
     * @param item 待入队的右值数据元素
     * @return true 成功入队；false 队列已被取消或因 DROP_NEWEST 策略被拒绝
     */
    bool push(T &&item) {
        return push_internal(std::move(item));
    }

    /**
     * @brief 原位构造数据入队
     */
    template <typename... Args>
    bool emplace(Args &&...args) {
        std::optional<T> discarded;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            if (cancelled_) return false;

            if (queue_.size() >= capacity_) {
                if (strategy_ == OverflowStrategy::drop_oldest) {
                    discarded = std::move(queue_.front());
                    queue_.pop_front();
                } else if (strategy_ == OverflowStrategy::drop_newest) {
                    return false;
                } else { // block
                    not_full_.wait(lock, [this] {
                        return cancelled_ || queue_.size() < capacity_;
                    });
                    if (cancelled_) return false;
                }
            }

            queue_.emplace_back(std::forward<Args>(args)...);
            not_empty_.notify_one();
        }
        return true;
    }

    /**
     * @brief 带超时的数据出队
     *
     * ⚠️ 锁外析构：出队元素先移入局部暂存，解锁后才赋给 `item`。`item` 原有的值
     *    （例如调用方复用的 HardwareFrame 句柄）因此在队列锁外析构。
     *
     * @tparam Rep 时钟周期表示类型
     * @tparam Period 时钟周期比例类型
     * @param[out] item 成功时接收出队数据的引用
     * @param timeout 最大等待超时时长
     * @return true 成功获取数据；false 等待超时或队列已被取消
     */
    template <typename Rep, typename Period>
    bool pop(T &item, const std::chrono::duration<Rep, Period> &timeout) {
        std::optional<T> popped;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            if (!not_empty_.wait_for(lock, timeout, [this] {
                return cancelled_ || !queue_.empty();
            })) {
                return false; // 等待超时
            }
            if (!take_front_locked(popped)) {
                return false; // 队列已被取消或当前为空
            }
        }
        item = std::move(*popped);
        return true;
    }

    /**
     * @brief 阻塞直到出队成功或队列被取消
     *
     * ⚠️ 锁外析构语义同带超时版本。
     *
     * @param[out] item 成功时接收出队数据的引用
     * @return true 成功获取数据；false 队列已被取消
     */
    bool pop(T &item) {
        std::optional<T> popped;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            not_empty_.wait(lock, [this] {
                return cancelled_ || !queue_.empty();
            });
            if (!take_front_locked(popped)) {
                return false;
            }
        }
        item = std::move(*popped);
        return true;
    }

    /**
     * @brief 协作式取消队列，唤醒所有阻塞等待的生产者与消费者
     *
     * ⚠️ 不可逆性契约：
     *   - 一旦取消，cancelled_ 恒为 true，队列不再接受新数据、pop 不再交出任何残余元素，且无法复活；
     *   - 残余数据在锁外释放，清空内存。
     *
     * ⚠️ 两阶段：先置位取消并广播（不分配、不抛出），再把残余元素移出锁外销毁。
     *    第二阶段托管用的 `std::deque` 默认构造在内存紧张时可能抛 `bad_alloc`，
     *    但此时取消已经生效、等待者已被唤醒——抛出只意味着残余元素延后到队列析构时释放。
     *    noexcept 线程入口的异常收口路径调用它时仍须自己 `try/catch`（见 `DecodeSession::decode_loop`）。
     */
    void cancel() {
        mark_cancelled();
        drain();
    }

    /**
     * @brief 仅清空残余元素，不改变取消标记
     *
     * 适用于拥塞重同步场景（如跳过非关键帧包），残余数据在锁外析构。
     * 即使在停机竞态下调用也不会意外复活已取消的队列。
     */
    void clear() {
        drain();
    }

    /**
     * @brief 取走全部残余元素（不改变取消标记），交由调用方在锁外处置
     *
     * 与 clear() 相同的锁外语义，但把元素交还给调用方（例如回收包缓冲进复用池）。
     */
    std::deque<T> drain() {
        std::deque<T> discarded;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            discarded.swap(queue_); // swap 不分配、不抛出
        }
        not_full_.notify_all();
        return discarded;
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
    size_t capacity() const noexcept {
        return capacity_;
    }

private:
    /** @brief 置位取消标记并唤醒全部等待者（不分配、不抛出） */
    void mark_cancelled() noexcept {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            cancelled_ = true;
        }
        not_empty_.notify_all();
        not_full_.notify_all();
    }

    /** @brief 持锁取出队头；已取消或为空时返回 false（取消后绝不交出残余元素） */
    bool take_front_locked(std::optional<T> &out) {
        if (cancelled_ || queue_.empty()) {
            return false;
        }
        out.emplace(std::move(queue_.front()));
        queue_.pop_front();
        not_full_.notify_one();
        return true;
    }

    template <typename U>
    bool push_internal(U &&item) {
        std::optional<T> discarded;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            if (cancelled_) return false;

            if (queue_.size() >= capacity_) {
                if (strategy_ == OverflowStrategy::drop_oldest) {
                    discarded = std::move(queue_.front());
                    queue_.pop_front();
                } else if (strategy_ == OverflowStrategy::drop_newest) {
                    return false;
                } else { // block
                    not_full_.wait(lock, [this] {
                        return cancelled_ || queue_.size() < capacity_;
                    });
                    if (cancelled_) return false;
                }
            }

            queue_.push_back(std::forward<U>(item));
            not_empty_.notify_one();
        }
        return true;
    }

    const size_t capacity_;                 /**< 队列容量上限 */
    const OverflowStrategy strategy_;       /**< 溢出淘汰策略 */
    mutable std::mutex mutex_;              /**< 保护内部 deque 与状态 */
    std::condition_variable not_empty_;     /**< 队列非空条件变量 */
    std::condition_variable not_full_;      /**< 队列非满条件变量 */
    std::deque<T> queue_;                   /**< 元素容器 */
    bool cancelled_{false};                 /**< 不可逆取消状态标志 */
};

} // namespace zhulong
