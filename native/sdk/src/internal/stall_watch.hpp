/**
 * @file stall_watch.hpp
 * @brief 单路 Worker 的无进展看门狗：让「卡在推理里」从不可见变成可诊断、可恢复
 *
 * 病根：`process()` 是一次同步调用，卡在里面（死锁、驱动 hang、无界循环）的线程
 * 既不读任何标志位，也不会发 `MSG_FRAME_ACK`。因此**没有任何协作式手段能回收它**：
 * 「停 ACK 让 broker 驱逐租约」这种事做不到 —— ACK 正是那个卡住的线程发的。
 *
 * 能做的只有两件：
 *   1. 让卡死**可见**：现在宿主侧完全看不见（broker 只按 in-flight 配额默默丢帧，
 *      而 `engine.h` 没有 client-stats ABI）；
 *   2. 让它**可恢复**：卡死线程无法被安全终止（C++ 没有线程抢占，`pthread_cancel`
 *      在持锁/未展开栈的 native 代码上是灾难），所以唯一干净的手段是让**整个进程**
 *      退出，由宿主按既有退避 / CRASH_LOOP 熔断 / 版本回滚链路拉起。
 *
 * ⚠️ 计时判据只有一条：**「已收到帧、尚未 ACK」的这段时间**。
 *    相机断流时 Worker 会长期空转在 500ms `recv` 超时循环里 —— 那是正常的，不是卡死。
 *    用「最后一次 ACK 之后过了多久」当判据会把断流相机全部误杀。
 */

#pragma once

#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>

namespace zhulong {
namespace sdk {
namespace detail {

/**
 * @brief 单路 Worker 被判定为无进展时的进程退出码
 *
 * 与 `kExitWorkerCrashed`(9) 刻意分开：9 是**确定性**异常（同版本重启大概率复现，
 * 会一路走到 CRASH_LOOP 与版本回滚），10 是**卡死**（资源竞争 / 驱动 hang，重启
 * 往往就能恢复）。宿主对两者的重启策略相同，但运维看到的是两种完全不同的故障。
 *
 * ⚠️ 与 Go 侧 `internal/algoipc.RunnerExitWorkerStalled` 严格相等；
 *    由 `internal/algorithm/ipc_contract_test.go` 执行跨语言比对。
 */
inline constexpr int kExitWorkerStalled = 10;

/** @brief 无进展阈值的默认值（毫秒）。取足够宽，避免把「慢」误判成「死」。 */
inline constexpr int64_t kDefaultStallTimeoutMs = 60000;

/**
 * @brief 阈值覆盖用的环境变量名
 *
 * 走环境变量而不是新的 argv 参数：宿主侧 `.env` 与 `ExtraEnv` 是既有通道，
 * 而 argv 契约（`--broker` / `--name` / `--algorithm-id`）与宿主 `ProcessConfig`
 * 保持不变。这是**保护参数**，不是业务配置 —— 业务参数才必须能从命令行复原。
 */
inline constexpr const char *kStallTimeoutEnv = "ZHULONG_WORKER_STALL_TIMEOUT_MS";

/** @brief 单调时钟毫秒（不受墙钟跳变影响，只看时长）。 */
inline int64_t monotonic_now_ms() noexcept {
    using namespace std::chrono;
    return duration_cast<milliseconds>(steady_clock::now().time_since_epoch()).count();
}

/**
 * @brief 解析阈值：缺失 / 非法一律回退默认值，显式 `0` 或负数表示关闭看门狗
 *
 * 「非法回退默认」而不是「非法即关闭」：拼错的环境变量名/值不该静默关掉保护。
 * 要关就显式写 0，那是一个能被 grep 到的决定。
 */
inline int64_t resolve_stall_timeout_ms(const char *raw) noexcept {
    if (raw == nullptr || *raw == '\0') {
        return kDefaultStallTimeoutMs;
    }
    char *end = nullptr;
    const long long parsed = std::strtoll(raw, &end, 10);
    if (end == raw || (end != nullptr && *end != '\0')) {
        return kDefaultStallTimeoutMs;
    }
    if (parsed <= 0) {
        return 0;
    }
    return static_cast<int64_t>(parsed);
}

/**
 * @brief 单路 Worker 的进展追踪器
 *
 * 只存一个原子时间戳，读侧（看门狗线程）与写侧（Worker 线程）无锁交互。
 */
class StallTracker {
public:
    /**
     * @brief RAII：进入「处理一帧」的作用域时构造，离开时自动结束计时
     *
     * 用 RAII 而不是手工配对，是因为这段代码里 `continue` / `break` / 异常退出
     * 的路径很多，漏掉任何一条都会让该路被永久判为「正在处理」，进而被看门狗误杀。
     */
    class ProcessingScope {
    public:
        explicit ProcessingScope(StallTracker &tracker) noexcept : tracker_(&tracker) {
            tracker_->begin_processing(monotonic_now_ms());
        }
        ~ProcessingScope() { tracker_->end_processing(); }

        ProcessingScope(const ProcessingScope &) = delete;
        ProcessingScope &operator=(const ProcessingScope &) = delete;
        ProcessingScope(ProcessingScope &&) = delete;
        ProcessingScope &operator=(ProcessingScope &&) = delete;

    private:
        StallTracker *tracker_;
    };

    void begin_processing(int64_t now_ms) noexcept {
        processing_since_ms_.store(now_ms, std::memory_order_relaxed);
    }

    void end_processing() noexcept {
        processing_since_ms_.store(kIdle, std::memory_order_relaxed);
    }

    /**
     * @brief 已停滞毫秒数；`0` 表示空闲（没有在途帧）或时钟倒退
     *
     * 时钟倒退（`now_ms <= since`）返回 0 而不是负数：单调时钟理论上不倒退，但
     * 一旦算出负值，上层 `>= timeout` 就永远不成立，保护会静默失效。
     */
    [[nodiscard]] int64_t stalled_ms(int64_t now_ms) const noexcept {
        const int64_t since = processing_since_ms_.load(std::memory_order_relaxed);
        if (since == kIdle || now_ms <= since) {
            return 0;
        }
        return now_ms - since;
    }

    [[nodiscard]] bool is_processing() const noexcept {
        return processing_since_ms_.load(std::memory_order_relaxed) != kIdle;
    }

private:
    static constexpr int64_t kIdle = 0;
    std::atomic<int64_t> processing_since_ms_{kIdle};
};

/**
 * @brief 输出 stall 记录并结束进程（不返回）
 *
 * ⚠️ 只允许一次**不分配**的 `::write`：
 *   - `_Exit` 会丢掉 log.cpp 异步 writer 队列里的记录，走日志通道等于什么都没留下；
 *   - 卡死发生时 `g_stderr_write_mutex` 很可能正被卡住的线程持有，走
 *     `write_runner_stderr` 就是死锁。
 * 用栈上 buffer + `snprintf`（`snprintf` 不分配内存）。
 */
[[noreturn]] inline void report_stall_and_exit(size_t idx, uint64_t stream_id,
                                               int64_t stalled_ms) noexcept {
    char line[192];
    const int n = std::snprintf(
        line, sizeof(line),
        "{\"status\":\"failed\",\"stage\":\"worker_stalled\",\"index\":%zu,"
        "\"stream_id\":%llu,\"stalled_ms\":%lld}\n",
        idx, static_cast<unsigned long long>(stream_id), static_cast<long long>(stalled_ms));
    if (n > 0) {
        const size_t len = static_cast<size_t>(n) < sizeof(line) ? static_cast<size_t>(n)
                                                                : sizeof(line) - 1;
        const ssize_t written = ::write(STDERR_FILENO, line, len);
        (void)written;
    }
    std::_Exit(kExitWorkerStalled);
}

/**
 * @brief 看门狗的采样间隔：阈值越短采得越密，但不少于 50ms、不多于 1s
 *
 * 下限 50ms 防止有人把阈值设成个位数毫秒时把核烧满；上限 1s 让判定误差不超过 1s。
 */
inline int64_t stall_poll_interval_ms(int64_t timeout_ms) noexcept {
    if (timeout_ms <= 0) {
        return 1000;
    }
    return std::max<int64_t>(50, std::min<int64_t>(timeout_ms / 4, 1000));
}

} // namespace detail
} // namespace sdk
} // namespace zhulong
