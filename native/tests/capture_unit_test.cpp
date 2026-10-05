/**
 * @file capture_unit_test.cpp
 * @brief RTSP URL 规范化、选项解析、内存借用视图与排空机制单元测试
 *
 * 验证目标：
 * 1. URL 规范化（RFC 3986 / RTSP 规范）：
 *    - 剥离前后空白、小写 scheme 和 host、IPv6 字面量保持、省略默认 554 端口、保留百分号合法编码；
 *    - 严格拒绝非法 scheme、无效端口、不完整百分号转义、fragment 锚点及格式错误等。
 * 2. 默认拉流选项（Options）解析正确性。
 * 3. 视频元数据与 extradata 借用视图（borrowed_view）内存布局一致性。
 * 4. 订阅通道排空（disable_and_drain）：
 *    - 验证排空后绝不再执行新的用户回调。
 * 5. 异常鲁棒性（Throwing consumer）：
 *    - 验证当用户回调抛出 C++ 异常时，in_callback 标志与排空条件变量能够被正确复原。
 */

#include "nodes/capture/rtsp_input.hpp"
#include "pipeline/engine.hpp"

#include <chrono>
#include <future>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>

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

        /* 4. 测试 Video extradata 借用视图一致性 */
        zhulong::Video video;
        video.extradata.resize(4096, 42);
        const auto view = video.borrowed_view();
        CHECK(view.extradata_size == 4096 && view.extradata[4095] == 42);

        /* 5. 测试订阅禁用与排空（disable_and_drain）：排空后即使调用 invoke 也不得触发回调 */
        int calls = 0;
        zhulong::Subscription subscription(1, [](uintptr_t token, const Zhulong_packet_view *) {
            ++*reinterpret_cast<int *>(token);
        }, reinterpret_cast<uintptr_t>(&calls));
        subscription.disable_and_drain();
        subscription.invoke({});
        CHECK(calls == 0);

        /* 6. 测试异常安全（Throwing consumer）：抛出异常的回调不得残留 active 状态或损坏 TLS 标记 */
        zhulong::Subscription throwing(1, [](uintptr_t, const Zhulong_packet_view *) { throw 1; }, 0);
        try {
            throwing.invoke({});
        } catch (...) {}
        throwing.disable_and_drain();
        CHECK(!zhulong::in_callback);

        std::cout << "URL/options/ownership/drain unit tests passed\n";
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
