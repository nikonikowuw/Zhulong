/**
 * @file worker_guard_test.cpp
 * @brief 单路 Worker 受控失败边界的单元测试
 *
 * 为什么值得单测：这条边界守的是「异常 → 受控退出码」的翻译，而真实触发源是
 * 骨架自身在 OOM 上的分配失败——生产与集成环境都无法可靠地复现一次 `bad_alloc`。
 * 直接测试边界的契约（捕获所有异常形态、handler 自身抛异常也不得逃逸）比伪造
 * 一次内存耗尽稳定得多。
 *
 * ⚠️ 本文件依赖 assert()，已登记在 CMakeLists 的 ASSERT_TEST_TARGETS（-UNDEBUG）。
 */

#include "internal/worker_guard.hpp"

#include <cassert>
#include <stdexcept>
#include <string>

namespace {

using zhulong::sdk::detail::kExitWorkerCrashed;
using zhulong::sdk::detail::run_worker_guarded;

/// 退出码必须与 Go 侧 algoipc.RunnerExitWorkerCrashed 相等（跨语言契约的另一半）。
void test_exit_code_value() { assert(kExitWorkerCrashed == 9); }

/// 正常完成：返回 true，且不得调用错误回调。
void test_normal_completion() {
    bool body_ran = false;
    bool error_called = false;
    const bool ok = run_worker_guarded(
        [&]() { body_ran = true; },
        [&](const char *) { error_called = true; });
    assert(ok);
    assert(body_ran);
    assert(!error_called);
}

/// std::exception：返回 false，并把 what() 原样交给回调。
void test_std_exception_is_captured() {
    std::string seen;
    const bool ok = run_worker_guarded(
        []() { throw std::runtime_error("bad_alloc simulated"); },
        [&](const char *detail) { seen = detail != nullptr ? detail : ""; });
    assert(!ok);
    assert(seen == "bad_alloc simulated");
}

/// 非 std 异常：同样受控，回调拿到非空兜底文本。
void test_non_std_exception_is_captured() {
    std::string seen;
    const bool ok = run_worker_guarded(
        []() { throw 42; },
        [&](const char *detail) { seen = detail != nullptr ? detail : ""; });
    assert(!ok);
    assert(!seen.empty());
}

/// 回调自身抛异常：边界是 noexcept，绝不能让它逃出去（否则 terminate 又回来了）。
void test_throwing_error_handler_does_not_escape() {
    const bool ok = run_worker_guarded(
        []() { throw std::runtime_error("original failure"); },
        [](const char *) { throw std::runtime_error("handler failed"); });
    assert(!ok);
}

/// 边界本身是 noexcept：这里用 static_assert 钉住，避免有人改成会抛的签名。
/// （C++17 不允许 lambda 出现在 nullptr 求值上下文，所以用具名函数。）
void noop_body() {}
void noop_error(const char *) {}

void test_boundary_is_noexcept() {
    static_assert(noexcept(run_worker_guarded(noop_body, noop_error)),
                  "run_worker_guarded must stay noexcept: an escaping exception would restore terminate()");
}

} // namespace

int main() {
    test_exit_code_value();
    test_normal_completion();
    test_std_exception_is_captured();
    test_non_std_exception_is_captured();
    test_throwing_error_handler_does_not_escape();
    test_boundary_is_noexcept();
    return 0;
}
