/**
 * @file stall_watch_test.cpp
 * @brief 无进展看门狗的判定契约单元测试
 *
 * 这里守的是两件容易写错、且错了代价很大的事：
 *   1. **判据**：只有「已收帧、未 ACK」才计时。用「距上次 ACK 多久」会把相机断流
 *      全部误判成卡死 —— 那是边缘部署里最常见的一种状态。
 *   2. **配置解析**：非法值必须回退默认（继续保护），只有显式 `0` 才关闭。
 *      拼错环境变量名就静默关掉保护，是最难发现的那种失效。
 *
 * ⚠️ 本文件依赖 assert()，已登记在 CMakeLists 的 ASSERT_TEST_TARGETS（-UNDEBUG）。
 */

#include "internal/stall_watch.hpp"

#include <cassert>

namespace {

using zhulong::sdk::detail::kDefaultStallTimeoutMs;
using zhulong::sdk::detail::kExitWorkerStalled;
using zhulong::sdk::detail::StallTracker;
using zhulong::sdk::detail::resolve_stall_timeout_ms;
using zhulong::sdk::detail::stall_poll_interval_ms;

/// 退出码必须与 Go 侧 algoipc.RunnerExitWorkerStalled 相等（跨语言契约的另一半）。
void test_exit_code_value() { assert(kExitWorkerStalled == 10); }

void test_missing_value_uses_default() {
    assert(resolve_stall_timeout_ms(nullptr) == kDefaultStallTimeoutMs);
    assert(resolve_stall_timeout_ms("") == kDefaultStallTimeoutMs);
}

/// 非法值回退默认而不是关闭：一个拼错的值不该让保护静默消失。
void test_invalid_value_falls_back_to_default() {
    assert(resolve_stall_timeout_ms("abc") == kDefaultStallTimeoutMs);
    assert(resolve_stall_timeout_ms("12x") == kDefaultStallTimeoutMs);
    assert(resolve_stall_timeout_ms("  ") == kDefaultStallTimeoutMs);
    assert(resolve_stall_timeout_ms("+") == kDefaultStallTimeoutMs);
}

/// 只有显式非正值才是「关闭」。
void test_explicit_disable() {
    assert(resolve_stall_timeout_ms("0") == 0);
    assert(resolve_stall_timeout_ms("-1") == 0);
    assert(resolve_stall_timeout_ms("-60000") == 0);
}

void test_valid_value_is_honoured() {
    assert(resolve_stall_timeout_ms("2500") == 2500);
    assert(resolve_stall_timeout_ms("1") == 1);
}

/// 空闲（无在途帧）永远不算卡死 —— 相机断流就是这个状态。
void test_idle_is_never_stalled() {
    StallTracker tracker;
    assert(!tracker.is_processing());
    assert(tracker.stalled_ms(0) == 0);
    assert(tracker.stalled_ms(10'000'000) == 0);
}

/// 处理窗口内按毫秒计量，结束后立刻回到空闲。
void test_processing_window_is_measured() {
    StallTracker tracker;
    tracker.begin_processing(1000);
    assert(tracker.is_processing());
    assert(tracker.stalled_ms(1000) == 0);
    assert(tracker.stalled_ms(1500) == 500);

    tracker.end_processing();
    assert(!tracker.is_processing());
    assert(tracker.stalled_ms(9000) == 0);
}

/// 时钟倒退返回 0 而不是负值：负值会让 `>= timeout` 永远不成立，保护静默失效。
void test_clock_going_backwards_is_clamped() {
    StallTracker tracker;
    tracker.begin_processing(2000);
    assert(tracker.stalled_ms(1000) == 0);
}

/// RAII：离开作用域（含 continue / break / 异常）必须自动结束计时。
void test_scope_pairs_automatically() {
    StallTracker tracker;
    {
        StallTracker::ProcessingScope scope(tracker);
        assert(tracker.is_processing());
    }
    assert(!tracker.is_processing());
}

void test_poll_interval_bounds() {
    assert(stall_poll_interval_ms(0) == 1000);
    assert(stall_poll_interval_ms(4) == 50);
    assert(stall_poll_interval_ms(1000) == 250);
    assert(stall_poll_interval_ms(60'000) == 1000);
}

} // namespace

int main() {
    test_exit_code_value();
    test_missing_value_uses_default();
    test_invalid_value_falls_back_to_default();
    test_explicit_disable();
    test_valid_value_is_honoured();
    test_idle_is_never_stalled();
    test_processing_window_is_measured();
    test_clock_going_backwards_is_clamped();
    test_scope_pairs_automatically();
    test_poll_interval_bounds();
    return 0;
}
