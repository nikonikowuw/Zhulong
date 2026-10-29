/**
 * @file worker_guard.hpp
 * @brief 单路 Worker 线程的受控失败边界：未捕获异常 → 有退出码的整包受控退出
 *
 * 为什么需要它：`PluginApi::process` 的 try-catch 只覆盖**算法类**抛出的异常。
 * 骨架自己的代码同样会抛——`serialize_inference_result` 每帧构造 `ostringstream`、
 * 结果数组与 socket 路径字符串增长，都可能抛 `std::bad_alloc` / `std::length_error`。
 *
 * 那些异常一旦逃出线程函数，C++ 直接 `std::terminate` → SIGABRT(134)，后果是：
 *   1. 没有退出码语义，宿主只能记一句 "runner process exited unexpectedly"；
 *   2. 没有 stage 日志，运维看不出是哪一路、什么原因；
 *   3. 已连上的其他路被**无声**连坐 —— 它与「一路失败整包退出（D6）」长得像，
 *      实则完全不同：D6 至少是可诊断、可退避、可熔断的受控失败。
 *
 * 本头把「异常」翻译回 D6 的受控失败路径：调用方在 `on_error` 里置 `stop_all` 与
 * `kExitWorkerCrashed`，进程仍走宿主既有的退避 / CRASH_LOOP 熔断 / 版本回滚链路。
 *
 * ⚠️ 不改变故障域语义：单路异常仍然是「整包退出」。理由与 D6 一致——让宿主以
 *    几秒钟的重启换回全部路，远好于一路静默缺失而宿主以为一切正常。
 */

#pragma once

#include <exception>
#include <utility>

namespace zhulong {
namespace sdk {
namespace detail {

/**
 * @brief 单路 Worker 自身代码抛出未捕获异常时的进程退出码
 *
 * ⚠️ 与 Go 侧 `internal/algoipc.RunnerExitWorkerCrashed` 严格相等；
 *    由 `internal/algorithm/ipc_contract_test.go` 执行跨语言比对。
 */
inline constexpr int kExitWorkerCrashed = 9;

/**
 * @brief 在异常边界内执行一路 Worker 的完整工作体
 *
 * @param body     单路 Worker 的工作体（连接、握手、收帧循环）；正常返回表示该路结束
 * @param on_error 捕获到异常时调用，入参为异常描述（`what()` 或非 std 异常的兜底文本）。
 *                 该指针**仅在本次调用期间有效**，需要保留请自行拷贝。
 * @return true 表示 body 正常返回；false 表示捕获到异常，进程应受控退出
 *
 * ⚠️ `on_error` 自身也可能抛（典型是 OOM 路径上构造诊断字符串）。本函数是 noexcept，
 *    异常从它里面逃出去等于把「受控退出」重新变回 terminate，所以这里对 `on_error`
 *    再做一层兜底。代价是那一刻的诊断日志会丢——因此调用方应当**先置状态位再写日志**，
 *    让 `stop_all` / 退出码在任何情况下都是准确的。
 */
template <typename Body, typename OnError>
bool run_worker_guarded(Body &&body, OnError &&on_error) noexcept {
    try {
        std::forward<Body>(body)();
        return true;
    } catch (const std::exception &error) {
        try {
            on_error(error.what());
        } catch (...) {
        }
        return false;
    } catch (...) {
        try {
            on_error("non-std exception");
        } catch (...) {
        }
        return false;
    }
}

} // namespace detail
} // namespace sdk
} // namespace zhulong
