/**
 * @file algo_main.hpp
 * @brief 算法包可执行文件骨架：一行宏把算法类变成包根可执行文件
 *
 * 交付形态（方案 A）：算法包 = 包根可执行文件 + 自带静态链接运行时。
 * 宿主只负责 `exec` 这个文件并用 FrameBroker 送帧，**不再** dlopen 任何 `.so`。
 *
 * 算法作者只需要写算法类，broker 连接、共享内存映射、心跳/ACK、底库同步、
 * 结果序列化与退出码全部由本骨架承担：
 *
 *     #include <zhulong/algo_main.hpp>
 *     class MyDetector { ... };            // init / create_session / detect
 *     ZHULONG_ALGO_MAIN(MyDetector, zhulong::sdk::DetectionResultAdapter,
 *                       "MyDetector", "1.0.0")
 *
 * ⚠️ 骨架与 SDK 里的 runner 语义必须**逐字一致**（stage token、退出码表、ACK 时机）：
 *    宿主看门狗与 Go 侧 `validRunnerStage` allowlist 依赖这些字符串。
 */

#pragma once

#include <zhulong/algo.hpp>
#include <zhulong/plugin.h>

#include <cstdio>
#include <string>
#include <vector>

namespace zhulong {
namespace sdk {

/**
 * @brief 骨架的启动参数（由 argv 解析，不读任何配置文件）
 *
 * 刻意不接受 `--config-file` 之类的旁路配置通道：算法包的运行参数必须全部来自
 * 宿主 `exec` 时的 argv，否则运维排查时「进程到底跑的是哪套配置」无法从命令行复原。
 * `--config <json>` 是这条立场的具体形态：值是 inline JSON（argv 可见），**不是**文件路径。
 * 它只承载**进程级**配置（一包一份）；任务级（逐路流）参数走 `MSG_TASK_CONFIG`。
 */
struct Options {
    std::vector<std::string> brokers;   /**< `--broker <sock>` 可重复；一路 broker 一个 Worker 线程 */
    std::string client_name{"algo_worker"}; /**< `--name`，IPC 握手身份 */
    std::string algorithm_id;           /**< `--algorithm-id`，写进结果载荷；也是 MSG_TASK_CONFIG 的过滤键 */
    std::string config_json{"{}"};       /**< `--config <json>`，进程级配置，一次性传给 Plugin::init */
    std::string sample_path;            /**< `--verify --sample <path>`，PPM/PGM 才会被真实解码 */
    bool verify{false};                 /**< 单次验包模式（不连 broker） */
    bool extract_feature{false};        /**< `--extract-feature`：stdin 帧流 → stdout 特征行 */
    int max_frames{-1};                 /**< `--max-frames <N>`，<0 表示不限（联调/自测用） */
    bool disable_motion_gate{false};    /**< `--disable-motion-gate`：禁用动检门禁（违停/在岗等时序算法全帧推理且永不发送伪消失信标） */
};

namespace detail {

/** @brief 解析 argv；遇到未知参数返回 false 并填充 error（fail closed，不猜） */
bool parse_options(int argc, char** argv, Options& options, std::string& error);

/** @brief 输出首行 `{"step":"start","plugin":...}`（宿主的验包解析器依赖它） */
void print_start_line(const std::string& plugin_label);

/**
 * @brief 把协议通道从 stdout 的噪声里隔离出来（幂等）
 *
 * 必须在任何厂商运行库初始化之前调用：厂商库会往 stdout 打启动横幅（实测 librga
 * 打印 `rga_api version ...`），而 stdout 是宿主按行解析的协议通道，一行噪声就会
 * 被判成 `protocol desync` 并杀掉热进程。做法是 dup 一份 fd 专供协议，再把 fd1
 * 改道 stderr（噪声归日志）。
 *
 * @return false 表示 dup/fdopen/dup2 失败（此时进程不能再安全地输出协议）
 */
bool install_protocol_channel();

/**
 * @brief 用已构造好的 C ABI 表执行骨架
 *
 * verify 模式：model_create → worker_create → task_open → process → release 一次，
 * 输出与 IPC 结果载荷同构的 JSON（多出 status/sample/width/height）。
 * extract-feature 模式：stdin 帧流 → `zhulong_face_extract_feature`（弱符号）→ stdout 行。
 * 否则：连所有 `--broker`，每路一个 Worker 线程长驻消费，退出码见 `run_ipc_workers` 注释。
 */
int run_api(const zhulong_plugin_api_v1& api, const Options& options);

} // namespace detail

/**
 * @brief 把算法类 + 结果适配器绑成可执行文件入口
 *
 * @tparam PluginClass 算法类（init / create_session / 任务方法）
 * @tparam ResultAdapter `zhulong::detail::{Detection,Classification,PersonStructure,
 *         VehicleStructure}ResultAdapter` 之一
 */
template<typename PluginClass, typename ResultAdapter>
int run(int argc, char** argv, const char* plugin_name, const char* plugin_version) {
    // ⚠️ 协议通道必须最先建立：它只依赖 fd，不依赖任何插件/模型状态；放在 get_api 之前
    //    也能覆盖「API 表构造期间厂商库就已打横幅」的情况。
    if (!detail::install_protocol_channel()) {
        std::fprintf(stderr,
                     "{\"status\":\"failed\",\"stage\":\"protocol\",\"error\":\"cannot isolate stdout\"}\n");
        return 5;
    }

    zhulong_plugin_api_v1 api{};
    const zhulong_status_t rc = zhulong::detail::PluginApi<PluginClass, ResultAdapter>::get_api(
        1, &api, sizeof(api), plugin_name, plugin_version);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "{\"status\":\"failed\",\"stage\":\"api_table\",\"rc\":%d}\n", static_cast<int>(rc));
        return 1;
    }

    Options options;
    std::string error;
    if (!detail::parse_options(argc, argv, options, error)) {
        std::fprintf(stderr, "{\"status\":\"failed\",\"stage\":\"args\",\"error\":\"%s\"}\n", error.c_str());
        return 5;
    }
    if (options.verify) {
        // 验包输出与迁移期 runner 同构：首行 start（plugin 字段报自身可执行文件路径）
        detail::print_start_line(argc > 0 && argv[0] != nullptr ? argv[0] : "");
    }
    return detail::run_api(api, options);
}

} // namespace sdk
} // namespace zhulong

/**
 * @brief 生成唯一 `main`（同一目标里只允许出现一次）
 */
#define ZHULONG_ALGO_MAIN(PluginClass, ResultAdapter, PluginName, PluginVersion) \
    int main(int argc, char** argv) {                                            \
        return ::zhulong::sdk::run<PluginClass, ResultAdapter>(                  \
            argc, argv, PluginName, PluginVersion);                              \
    }
