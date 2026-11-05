/**
 * @file capture_unit_test.cpp
 * @brief RTSP URL 规范化、选项解析、内存借用视图与排空机制单元测试
 *
 * 验证目标：
 * 1. URL 规范化（RFC 3986 / RTSP 规范）：
 *    - 剥离前后空白、小写 scheme 和 host、IPv6 字面量保持、省略默认 554 端口、保留百分号合法编码；
 *    - 严格拒绝非法 scheme、无效端口、不完整百分号转义、fragment 锚点及格式错误等。
 * 2. 默认拉流选项（Options）解析正确性。
 * 3. 取消与超时状态机（Cancellation）：默认不超时、零超时立即可见、刷新后恢复。
 * 4. 视频元数据与 extradata 借用视图（borrowed_view）内存布局一致性。
 * 5. 订阅通道排空（disable_and_drain）：
 *    - 验证排空后绝不再执行新的用户回调。
 * 6. 异常鲁棒性（Throwing consumer）：
 *    - 验证当用户回调抛出 C++ 异常时，in_callback 标志与排空条件变量能够被正确复原。
 */

#include "nodes/capture/rtsp_input.hpp"
#include "pipeline/engine.hpp"

#include <atomic>
#include <chrono>
#include <future>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>
#include <tuple>

#define CHECK(condition) do { if (!(condition)) throw std::runtime_error("check failed at line " + std::to_string(__LINE__)); } while (false)
using namespace std::chrono_literals;

int main() {
    try {
        using zhulong::normalize_rtsp_url;

        /* 1. 测试合法 URL 规范化逻辑 */
        CHECK(normalize_rtsp_url(" \tRTSP://User:p%40ss@EXAMPLE.Com:0554/Case?X=Y+Z\n") == "rtsp://User:p%40ss@example.com/Case?X=Y+Z");
        CHECK(normalize_rtsp_url("rtsp://[ABCD::1]:8554/A") == "rtsp://[abcd::1]:8554/A");
        CHECK(normalize_rtsp_url("rtsp://host/a@b?c=@d") == "rtsp://host/a@b?c=@d");

        /* 2. 测试非法 URL 拒绝场景 */
        for (const auto *url : {
            "",                      // 空串
            "http://host/A",         // 非 RTSP scheme
            "rtsp:///A",             // 缺失 host
            "rtsp://a:b@c@host/A",   // 歧义的多个 @
            "rtsp://host/a b",       // 未转义的空格
            "   ",                  // 仅空白字符，剥离后为空
            "rtsp://",              // 仅 scheme，缺主机
            "rtsp://host/a%2",       // 不完整的百分号转义
            "rtsp://host/a#b",       // RTSP 不支持 fragment (#)
            "rtsp://host:0/A",       // 端口为 0
            "rtsp://host:65536/A",   // 端口超出 65535 范围
            "rtsp://host:/A",        // 空端口号
            "rtsp://::1/A",          // 未使用方括号包裹的裸 IPv6 地址
            "rtsp://[::1]junk/A"     // 非法的 IPv6 闭合后字符
        }) {
            bool rejected = false;
            try {
                normalize_rtsp_url(url);
            } catch (const zhulong::Failure &failure) {
                rejected = failure.status == Zhulong_ERR_INVALID_ARGUMENT;
            }
            CHECK(rejected);
        }

        /* 3. 测试选项解析与默认值 */
        const auto defaults = zhulong::parse_options(nullptr);
        CHECK(defaults.transport == Zhulong_TRANSPORT_TCP);
        CHECK(defaults.open_timeout_ms == 5000 && defaults.idle_timeout_ms == 5000);

        /* 4. 测试取消与超时状态机（默认不超时 / 显式超时 / 停止标志） */
        zhulong::Cancellation cancellation;
        CHECK(!cancellation.expired());      // 默认构造不得被误判为已过期
        CHECK(!cancellation.interrupted());
        cancellation.expire_after(0);         // 零超时等价于立即过期
        CHECK(cancellation.expired() && cancellation.interrupted());
        cancellation.expire_after(60000);     // 刷新为未来截止点后恢复未过期
        CHECK(!cancellation.expired() && !cancellation.interrupted());
        cancellation.stopped.store(true);
        CHECK(cancellation.interrupted());

        /* 5. 测试 Video extradata 借用视图一致性 */
        zhulong::Video video;
        video.extradata.resize(4096, 42);
        const auto view = video.borrowed_view();
        CHECK(view.extradata_size == 4096 && view.extradata[4095] == 42);

        /* 6. 测试订阅禁用与排空（disable_and_drain）：排空后即使调用 invoke 也不得触发回调 */
        int calls = 0;
        zhulong::Subscription subscription(1, [](uintptr_t token, const Zhulong_packet_view *) {
            ++*reinterpret_cast<int *>(token);
        }, reinterpret_cast<uintptr_t>(&calls));
        subscription.disable_and_drain();
        subscription.invoke({});
        CHECK(calls == 0);

        /* 7. 测试异常安全（Throwing consumer）：抛出异常的回调不得残留 active 状态或损坏 TLS 标记 */
        zhulong::Subscription throwing(1, [](uintptr_t, const Zhulong_packet_view *) { throw 1; }, 0);
        try {
            throwing.invoke({});
        } catch (...) {}
        throwing.disable_and_drain();
        CHECK(!zhulong::in_callback);

        /* 8. 测试 InCallbackGuard 嵌套与恢复 */
        {
            CHECK(!zhulong::in_callback);
            {
                zhulong::InCallbackGuard outer;
                CHECK(zhulong::in_callback);
                {
                    zhulong::InCallbackGuard inner;
                    CHECK(zhulong::in_callback);
                }
                CHECK(zhulong::in_callback);
            }
            CHECK(!zhulong::in_callback);
        }

        /* 9. 测试 AISubscription 回调防重入标记与异常安全 */
        {
            bool cb_verified = false;
            zhulong::AISubscription ai_sub(1, [](uintptr_t tok, Zhulong_stream_id, const char*, size_t) {
                CHECK(zhulong::in_callback);
                *reinterpret_cast<bool*>(tok) = true;
            }, reinterpret_cast<uintptr_t>(&cb_verified));
            ai_sub.invoke(1, "{}", 2);
            CHECK(cb_verified);
            CHECK(!zhulong::in_callback);

            zhulong::AISubscription ai_throwing(1, [](uintptr_t, Zhulong_stream_id, const char*, size_t) {
                CHECK(zhulong::in_callback);
                throw std::runtime_error("ai cb error");
            }, 0);
            bool caught = false;
            try {
                ai_throwing.invoke(1, "{}", 2);
            } catch (...) {
                caught = true;
            }
            CHECK(caught);
            CHECK(!zhulong::in_callback);
            CHECK(!ai_throwing.is_enabled());
        }

        /* 10. 测试并发多回调执行期间 disable_and_drain 的排空屏障（活动计数器） */
        {
            std::atomic<int> active_in_cb{0};
            std::atomic<bool> release_cb{false};
            std::atomic<int> completed_cbs{0};

            auto sub = std::make_shared<zhulong::AISubscription>(
                1,
                [](uintptr_t tok, Zhulong_stream_id, const char*, size_t) {
                    auto *ctx = reinterpret_cast<std::tuple<std::atomic<int>*, std::atomic<bool>*, std::atomic<int>*>*>(tok);
                    auto *active = std::get<0>(*ctx);
                    auto *release = std::get<1>(*ctx);
                    auto *completed = std::get<2>(*ctx);

                    active->fetch_add(1);
                    while (!release->load()) {
                        std::this_thread::yield();
                    }
                    active->fetch_sub(1);
                    completed->fetch_add(1);
                },
                0
            );
            auto ctx = std::make_tuple(&active_in_cb, &release_cb, &completed_cbs);
            sub->token = reinterpret_cast<uintptr_t>(&ctx);

            std::thread t1([&] { sub->invoke(1, "{}", 2); });
            std::thread t2([&] { sub->invoke(1, "{}", 2); });

            // 等待两个线程都进入回调
            while (active_in_cb.load() < 2) {
                std::this_thread::yield();
            }

            std::atomic<bool> drain_finished{false};
            std::thread drain_thread([&] {
                sub->disable_and_drain();
                drain_finished.store(true);
            });

            // 给 drain 线程时间启动并等待
            std::this_thread::sleep_for(std::chrono::milliseconds(20));
            CHECK(!drain_finished.load());

            // 释放回调
            release_cb.store(true);

            drain_thread.join();
            t1.join();
            t2.join();

            CHECK(drain_finished.load());
            CHECK(completed_cbs.load() == 2);
            CHECK(!sub->is_enabled());
        }

        std::cout << "URL/options/ownership/drain unit tests passed\n";
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
