#include "Zhulong/engine.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <cassert>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <string>
#include <thread>

namespace {

// 静默本地 TCP 监听桩：只完成内核三次握手、不回发 RTSP 响应，使测试流在 open_timeout 内
// 稳定保持 CONNECTING，避免向无人监听端口发包触发瞬时 ECONNREFUSED 导致流提前进入 FAILED
// 从而把 decode_sessions_in_use 扣回 0（破坏容量准入用例确定性）。
class ScopedSilentTcpListener {
public:
    ScopedSilentTcpListener() = default;
    ~ScopedSilentTcpListener() { close_fd(); }

    ScopedSilentTcpListener(const ScopedSilentTcpListener &) = delete;
    ScopedSilentTcpListener &operator=(const ScopedSilentTcpListener &) = delete;

    bool start() {
        fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
        if (fd_ < 0) return false;

        sockaddr_in addr{};
        addr.sin_family = AF_INET;
        addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        addr.sin_port = 0;
        if (::bind(fd_, reinterpret_cast<const sockaddr *>(&addr), sizeof(addr)) != 0) {
            close_fd();
            return false;
        }
        if (::listen(fd_, 16) != 0) {
            close_fd();
            return false;
        }

        socklen_t len = sizeof(addr);
        if (::getsockname(fd_, reinterpret_cast<sockaddr *>(&addr), &len) != 0) {
            close_fd();
            return false;
        }
        port_ = ntohs(addr.sin_port);
        return port_ > 0;
    }

    uint16_t port() const noexcept { return port_; }

    std::string rtsp_url(const char *path) const {
        return "rtsp://127.0.0.1:" + std::to_string(port_) + path;
    }

private:
    void close_fd() noexcept {
        if (fd_ >= 0) {
            ::close(fd_);
            fd_ = -1;
        }
    }

    int fd_{-1};
    uint16_t port_{0};
};

} // namespace

int main() {
    printf("[TEST] Starting decode admission and fail-closed tests...\n");

    ScopedSilentTcpListener silent_rtsp;
    if (!silent_rtsp.start()) {
        fprintf(stderr, "Failed to start local silent TCP listener\n");
        return 1;
    }

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
    invalid_policy.backend = 4; // 超出枚举范围 (0=auto, 1=mpp, 2=ffmpeg, 3=ascend)
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
    opts.open_timeout_ms = 5000;
    opts.idle_timeout_ms = 5000;

    const std::string url_mpp_fail = silent_rtsp.rtsp_url("/test_mpp_fail");
    const std::string url_preview_1 = silent_rtsp.rtsp_url("/preview_1");
    const std::string url_preview_2 = silent_rtsp.rtsp_url("/preview_2");
    const std::string url_ai_1 = silent_rtsp.rtsp_url("/ai_stream_1");
    const std::string url_ai_2 = silent_rtsp.rtsp_url("/ai_stream_2");

    Zhulong_stream_id stream_id = 0;
    // 请求 AI 消费者拉流：应当被静态硬件门控立即拦截返回 Zhulong_ERR_HARDWARE_UNAVAILABLE
    Zhulong_status_t status = Zhulong_stream_acquire(
        engine, url_mpp_fail.c_str(), &opts, 1001, Zhulong_CONSUMER_AI, &stream_id
    );
    if (status != Zhulong_ERR_HARDWARE_UNAVAILABLE) {
        fprintf(stderr, "Expected ERR_HARDWARE_UNAVAILABLE (-15), got: %d\n", status);
        return 12;
    }

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 13;
    if (stats.rejected_requests != 1 || stats.active_sessions != 0) return 13;

    // 3.1 测试昇腾硬件探针与 Fail-Closed 门控
    // 在 x86 主机上未配置 CANN 时，昇腾解码不可用。当配置强制昇腾且禁止降级时，AI 拉流必须同步阻断。
    Zhulong_decode_policy_t ascend_strict_policy{};
    ascend_strict_policy.backend = Zhulong_DECODE_BACKEND_ASCEND;
    ascend_strict_policy.allow_software_fallback = 0;
    ascend_strict_policy.max_sessions = 4;
    if (Zhulong_engine_configure_decode(engine, &ascend_strict_policy) != Zhulong_OK) return 1301;

    const std::string url_ascend_fail = silent_rtsp.rtsp_url("/test_ascend_fail");
    status = Zhulong_stream_acquire(
        engine, url_ascend_fail.c_str(), &opts, 1002, Zhulong_CONSUMER_AI, &stream_id
    );
    if (status != Zhulong_ERR_HARDWARE_UNAVAILABLE) {
        fprintf(stderr, "Expected ERR_HARDWARE_UNAVAILABLE (-15) for Ascend, got: %d\n", status);
        return 1302;
    }

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 1303;
    if (stats.rejected_requests != 2 || stats.active_sessions != 0) return 1304;

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
        engine, url_preview_1.c_str(), &opts, 2001, Zhulong_CONSUMER_PREVIEW, &preview_stream_1
    );
    if (status != Zhulong_OK || preview_stream_1 == 0) return 15;

    status = Zhulong_stream_acquire(
        engine, url_preview_2.c_str(), &opts, 2002, Zhulong_CONSUMER_PREVIEW, &preview_stream_2
    );
    if (status != Zhulong_OK || preview_stream_2 == 0) return 15;

    // 预览流不占用物理解码流计数
    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 15;
    if (stats.active_sessions != 0) return 15;

    // 4.2 接入第 1 路 AI 流：成功扣减配额
    Zhulong_stream_id ai_stream_1 = 0;
    status = Zhulong_stream_acquire(
        engine, url_ai_1.c_str(), &opts, 3001, Zhulong_CONSUMER_AI, &ai_stream_1
    );
    if (status != Zhulong_OK || ai_stream_1 == 0) return 16;

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 16;
    if (stats.active_sessions != 1) {
        fprintf(stderr, "Expected active_sessions == 1, got: %d\n", stats.active_sessions);
        return 16;
    }

    // 4.3 接入第 2 路全新 AI 流：应当被容量门控拒绝返回 Zhulong_ERR_CAPACITY_EXCEEDED
    Zhulong_stream_id ai_stream_2 = 0;
    status = Zhulong_stream_acquire(
        engine, url_ai_2.c_str(), &opts, 3002, Zhulong_CONSUMER_AI, &ai_stream_2
    );
    if (status != Zhulong_ERR_CAPACITY_EXCEEDED) {
        fprintf(stderr, "Expected ERR_CAPACITY_EXCEEDED (-16), got: %d\n", status);
        return 17;
    }

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 18;
    if (stats.active_sessions != 1 || stats.rejected_requests != 3) return 18;

    // 4.4 既有预览流升级 AI 消费者门控拦截
    // preview_1 原先只有预览消费，现尝试为它追加 AI 消费者：因配额已满必须被拦截
    Zhulong_stream_id preview_upgrade_id = 0;
    status = Zhulong_stream_acquire(
        engine, url_preview_1.c_str(), &opts, 4001, Zhulong_CONSUMER_AI, &preview_upgrade_id
    );
    if (status != Zhulong_ERR_CAPACITY_EXCEEDED) {
        fprintf(stderr, "Expected ERR_CAPACITY_EXCEEDED on upgrade, got: %d\n", status);
        return 19;
    }
    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 20;
    if (stats.rejected_requests != 4) return 20;

    // 4.5 释放第 1 路 AI 消费者，验证配额原子归还
    status = Zhulong_stream_release(engine, ai_stream_1, 3001);
    if (status != Zhulong_OK) return 21;

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 21;
    if (stats.active_sessions != 0) return 21; // 配额立即回收

    // 4.6 归还后再次接入 AI 流：成功
    status = Zhulong_stream_acquire(
        engine, url_ai_2.c_str(), &opts, 3002, Zhulong_CONSUMER_AI, &ai_stream_2
    );
    if (status != Zhulong_OK || ai_stream_2 == 0) return 22;

    if (Zhulong_engine_get_decode_stats(engine, &stats) != Zhulong_OK) return 22;
    if (stats.active_sessions != 1) return 22;

    // 5. FAILED 流不得被复用：拉流失败后只要还有消费者持有它，按 URL 复用就会把新消费者
    //    挂到一条死流上，Go 侧「acquire → 发现 FAILED → Close → 重试」会永远拿回同一条死流。
    {
        const char *dead_url = "rtsp://127.0.0.1:1/failed_reuse"; // 端口 1：连接被拒，快速失败
        Zhulong_stream_id dead_id = 0;
        status = Zhulong_stream_acquire(engine, dead_url, &opts, 5001, Zhulong_CONSUMER_PREVIEW, &dead_id);
        if (status != Zhulong_OK || dead_id == 0) return 23;

        bool failed = false;
        for (int i = 0; i < 200 && !failed; ++i) {
            Zhulong_stream_status st{};
            if (Zhulong_stream_get_status(engine, dead_id, &st) != Zhulong_OK) return 24;
            failed = st.state == Zhulong_STREAM_FAILED;
            if (!failed) std::this_thread::sleep_for(std::chrono::milliseconds(20));
        }
        if (!failed) {
            fprintf(stderr, "stream to refused port never reached FAILED\n");
            return 25;
        }

        Zhulong_stream_id fresh_id = 0;
        status = Zhulong_stream_acquire(engine, dead_url, &opts, 5002, Zhulong_CONSUMER_PREVIEW, &fresh_id);
        if (status != Zhulong_OK || fresh_id == 0) return 26;
        if (fresh_id == dead_id) {
            fprintf(stderr, "acquire must not reuse a FAILED stream\n");
            return 27;
        }

        Zhulong_stream_release(engine, dead_id, 5001);
        Zhulong_stream_release(engine, fresh_id, 5002);
    }

    // 清理释放
    Zhulong_stream_release(engine, ai_stream_2, 3002);
    Zhulong_stream_release(engine, preview_stream_1, 2001);
    Zhulong_stream_release(engine, preview_stream_2, 2002);

    Zhulong_engine_stop(engine);
    Zhulong_engine_destroy(engine);

    printf("[TEST] Decode admission and fail-closed tests passed successfully!\n");
    return 0;
}
