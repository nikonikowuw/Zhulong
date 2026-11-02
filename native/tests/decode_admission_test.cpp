#include "Zhulong/engine.h"

#include <cassert>
#include <cstdio>
#include <cstring>
#include <thread>
#include <chrono>

int main() {
    printf("[TEST] Starting decode admission and fail-closed tests...\n");

    Zhulong_engine_h engine = nullptr;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || !engine) {
        fprintf(stderr, "Failed to create engine\n");
        return 1;
    }

    // 1. 测试 configure_decode 和 get_decode_stats 参数边界
    if (Zhulong_engine_configure_decode(nullptr, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 2;
    if (Zhulong_engine_configure_decode(engine, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 3;

    Zhulong_decode_policy_t invalid_policy = {-1, 0, 0};
    if (Zhulong_engine_configure_decode(engine, &invalid_policy) != Zhulong_ERR_INVALID_ARGUMENT) return 4;
    invalid_policy.backend = 3; // 超出枚举范围
    if (Zhulong_engine_configure_decode(engine, &invalid_policy) != Zhulong_ERR_INVALID_ARGUMENT) return 5;
    invalid_policy.backend = 0;
    invalid_policy.max_sessions = -1; // 非法负数
    if (Zhulong_engine_configure_decode(engine, &invalid_policy) != Zhulong_ERR_INVALID_ARGUMENT) return 6;

    if (Zhulong_engine_get_decode_stats(nullptr, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 7;
    if (Zhulong_engine_get_decode_stats(engine, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 8;

    Zhulong_decode_stats_t stats{};
    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 9;
    assert(stats.active_sessions == 0);
    assert(stats.rejected_requests == 0);

    // 2. 启动引擎
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 10;

    // 3. 测试静态硬件探针与 Fail-Closed 门控
    // 在 x86 主机上，MPP 硬件解码不可用。当配置强制 MPP 且禁止降级时，AI 拉流必须同步阻断。
    Zhulong_decode_policy_t mpp_strict_policy{};
    mpp_strict_policy.backend = Zhulong_DECODE_BACKEND_MPP;
    mpp_strict_policy.allow_software_fallback = 0;
    mpp_strict_policy.max_sessions = 4;
    if (Zhulong_engine_configure_decode(engine, &mpp_strict_policy) != Zhulong_OK) return 11;

    Zhulong_stream_options opts{};
    opts.transport = Zhulong_TRANSPORT_TCP;
    opts.open_timeout_ms = 1000;
    opts.idle_timeout_ms = 1000;

    Zhulong_stream_id stream_id = 0;
    // 请求 AI 消费者拉流：应当被静态硬件门控立即拦截返回 Zhulong_ERR_HARDWARE_UNAVAILABLE
    Zhulong_status_t status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/test_mpp_fail", &opts, 1001, Zhulong_CONSUMER_AI, &stream_id
    );
    if (status != Zhulong_ERR_HARDWARE_UNAVAILABLE) {
        fprintf(stderr, "Expected ERR_HARDWARE_UNAVAILABLE (-15), got: %d\n", status);
        return 12;
    }

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 13;
    assert(stats.rejected_requests == 1);
    assert(stats.active_sessions == 0);

    // 4. 测试容量准入控制 (使用 AUTO + 允许软解模式)
    Zhulong_decode_policy_t cap_policy{};
    cap_policy.backend = Zhulong_DECODE_BACKEND_AUTO;
    cap_policy.allow_software_fallback = 1;
    cap_policy.max_sessions = 1; // 仅限 1 路物理解码流
    if (Zhulong_engine_configure_decode(engine, &cap_policy) != Zhulong_OK) return 14;

    // 4.1 纯预览流不占用解码配额测试
    Zhulong_stream_id preview_stream_1 = 0;
    Zhulong_stream_id preview_stream_2 = 0;
    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/preview_1", &opts, 2001, Zhulong_CONSUMER_PREVIEW, &preview_stream_1
    );
    assert(status == Zhulong_OK && preview_stream_1 > 0);

    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/preview_2", &opts, 2002, Zhulong_CONSUMER_PREVIEW, &preview_stream_2
    );
    assert(status == Zhulong_OK && preview_stream_2 > 0);

    // 预览流不占用物理解码流计数
    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 15;
    assert(stats.active_sessions == 0);

    // 4.2 接入第 1 路 AI 流：成功扣减配额
    Zhulong_stream_id ai_stream_1 = 0;
    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/ai_stream_1", &opts, 3001, Zhulong_CONSUMER_AI, &ai_stream_1
    );
    assert(status == Zhulong_OK && ai_stream_1 > 0);

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 16;
    assert(stats.active_sessions == 1);

    // 4.3 接入第 2 路全新 AI 流：应当被容量门控拒绝返回 Zhulong_ERR_CAPACITY_EXCEEDED
    Zhulong_stream_id ai_stream_2 = 0;
    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/ai_stream_2", &opts, 3002, Zhulong_CONSUMER_AI, &ai_stream_2
    );
    if (status != Zhulong_ERR_CAPACITY_EXCEEDED) {
        fprintf(stderr, "Expected ERR_CAPACITY_EXCEEDED (-16), got: %d\n", status);
        return 17;
    }

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 18;
    assert(stats.active_sessions == 1);
    assert(stats.rejected_requests == 2); // 之前的 1 次 + 本次 1 次

    // 4.4 既有预览流升级 AI 消费者门控拦截
    // preview_1 原先只有预览消费，现尝试为它追加 AI 消费者：因配额已满必须被拦截
    Zhulong_stream_id preview_upgrade_id = 0;
    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/preview_1", &opts, 4001, Zhulong_CONSUMER_AI, &preview_upgrade_id
    );
    if (status != Zhulong_ERR_CAPACITY_EXCEEDED) {
        fprintf(stderr, "Expected ERR_CAPACITY_EXCEEDED on upgrade, got: %d\n", status);
        return 19;
    }
    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 20;
    assert(stats.rejected_requests == 3);

    // 4.5 释放第 1 路 AI 消费者，验证配额原子归还
    status = Zhulong_stream_release(engine, ai_stream_1, 3001);
    assert(status == Zhulong_OK);

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 21;
    assert(stats.active_sessions == 0); // 配额立即回收

    // 4.6 归还后再次接入 AI 流：成功
    status = Zhulong_stream_acquire(
        engine, "rtsp://127.0.0.1:8554/ai_stream_2", &opts, 3002, Zhulong_CONSUMER_AI, &ai_stream_2
    );
    assert(status == Zhulong_OK && ai_stream_2 > 0);

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 22;
    assert(stats.active_sessions == 1);

    // 清理释放
    Zhulong_stream_release(engine, ai_stream_2, 3002);
    Zhulong_stream_release(engine, preview_stream_1, 2001);
    Zhulong_stream_release(engine, preview_stream_2, 2002);

    Zhulong_engine_stop(engine);
    Zhulong_engine_destroy(engine);

    printf("[TEST] Decode admission and fail-closed tests passed successfully!\n");
    return 0;
}
