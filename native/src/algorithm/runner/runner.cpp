/**
 * @file runner.cpp
 * @brief 宿主侧算法 Runner：临时二进制，把 .so 插件包成 broker 消费者（**迁移期形态**）
 *
 * ⚠️ 交付形态是「算法包自带包根可执行文件」（阶段 4 起宿主只 exec 那个文件）。
 *    本文件保留的唯一目的，是在迁移期继续服务两类现存调用方：
 *      - Go 侧 `internal/algorunner` 的进程看护（`--plugin ... --broker ...`）；
 *      - Go 侧验包门禁（`--verify-plugin <so> --sample <path>`）。
 *    真正的骨架实现（broker 循环 / 验包 / 结果序列化）已迁到
 *    `native/sdk/src/algo_main.cpp` 并编进 `libzhulong_algo_sdk.a`；本文件只做
 *    「dlopen + 调用骨架」，不复制任何循环逻辑。
 *
 * 退出码表与 stage token 由骨架实现决定（见 algo_main.cpp 的 run_ipc_workers 注释）。
 */

#include <zhulong/algo_main.hpp>
#include <zhulong/plugin.h>

#include "common/log.hpp"
#include <dlfcn.h>

#include <cstring>
#include <iostream>
#include <string>
#include <vector>

namespace zhulong
{
    /**
     * @brief 插件动态库加载器（RAII 封装 dlopen/dlsym/dlclose 与 ABI 握手）
     *
     * 析构自动 unload；load 失败时保证已获得的句柄被释放，不泄漏 dlopen 引用。
     */
    class PluginLoader
    {
    public:
        ~PluginLoader()
        {
            unload();
        }

        /**
         * @brief 加载插件动态库并完成 v1 ABI 握手
         *
         * 以 RTLD_NOW | RTLD_LOCAL 加载（立即解析符号且不污染全局符号表），
         * 查找 ZHULONG_PLUGIN_ENTRY_POINT 并填充 zhulong_plugin_api_v1；
         * 要求 api_version == 1，否则视为不兼容并卸载。
         *
         * @param so_path 插件 .so 文件路径
         * @param[out] error_msg 失败时输出错误原因
         * @return true 加载并握手成功；false 失败（已回滚句柄）
         */
        bool load(const std::string &so_path, std::string &error_msg)
        {
            unload();
            handle_ = dlopen(so_path.c_str(), RTLD_NOW | RTLD_LOCAL);
            if (!handle_)
            {
                const char *err = dlerror();
                error_msg = err ? err : "unknown dlopen error";
                ZL_LOGE(0, "algorithm.runner", "plugin_load_failed reason=dlopen");
                return false;
            }

            auto get_api_fn = reinterpret_cast<zhulong_status_t (*)(uint32_t, void *, size_t)>(
                dlsym(handle_, ZHULONG_PLUGIN_ENTRY_POINT));
            if (!get_api_fn)
            {
                error_msg = "Symbol " ZHULONG_PLUGIN_ENTRY_POINT " not found in plugin";
                ZL_LOGE(0, "algorithm.runner", "plugin_load_failed reason=entry_symbol_missing");
                unload();
                return false;
            }

            std::memset(&api_, 0, sizeof(api_));
            zhulong_status_t rc = get_api_fn(1, &api_, sizeof(api_));
            if (rc != ZHULONG_PLUGIN_OK || api_.api_version != 1)
            {
                error_msg = "Plugin ABI handshake failed or version unsupported";
                ZL_LOGE(0, "algorithm.runner", "plugin_handshake_failed rc=%d api_version=%u",
                        static_cast<int>(rc), api_.api_version);
                unload();
                return false;
            }

            so_path_ = so_path;
            return true;
        }

        void unload()
        {
            if (handle_)
            {
                dlclose(handle_);
                handle_ = nullptr;
            }
            std::memset(&api_, 0, sizeof(api_));
        }

        bool is_loaded() const noexcept { return handle_ != nullptr; }
        const zhulong_plugin_api_v1 &api() const noexcept { return api_; }
        const std::string &path() const noexcept { return so_path_; }

    private:
        void *handle_{nullptr};
        zhulong_plugin_api_v1 api_{};
        std::string so_path_;
    };

    /**
     * @brief 一次性验包（迁移期入口）：dlopen 插件后交给骨架执行
     */
    int run_verify_plugin(const std::string &so_path, const std::string &sample_path)
    {
        sdk::detail::print_start_line(so_path);

        PluginLoader loader;
        std::string err;
        if (!loader.load(so_path, err))
        {
            sdk::detail::report_dlopen_failure(err);
            return 1;
        }
        sdk::Options options;
        options.verify = true;
        options.sample_path = sample_path;
        return sdk::detail::run_api(loader.api(), options);
    }

    /**
     * @brief 长驻消费（迁移期入口）：dlopen 插件后交给骨架执行
     */
    int run_ipc_workers(const std::string &so_path, const std::vector<std::string> &broker_socks,
                        const std::string &client_name, int max_frames = -1,
                        const std::string &algorithm_id = "")
    {
        PluginLoader loader;
        std::string err;
        if (!loader.load(so_path, err))
        {
            sdk::detail::report_dlopen_failure(err);
            return 1;
        }
        sdk::Options options;
        options.brokers = broker_socks;
        options.client_name = client_name;
        options.max_frames = max_frames;
        options.algorithm_id = algorithm_id;
        return sdk::detail::run_api(loader.api(), options);
    }

} // namespace zhulong

/**
 * @brief 日志写线程的 RAII 守卫（dlopen 阶段的诊断日志必须先能落盘）
 */
class RunnerLogWriterGuard
{
public:
    RunnerLogWriterGuard() noexcept : started_(zhulong::start_runner_log_writer()) {}
    ~RunnerLogWriterGuard()
    {
        if (started_) zhulong::stop_runner_log_writer();
    }
    bool started() const noexcept { return started_; }

private:
    bool started_;
};

/**
 * @brief Runner CLI 入口：解析命令行参数并分派到验包或 IPC 消费路径
 *
 * 无有效参数时打印用法说明并返回 0（不是错误，避免被看门狗误判为崩溃）。
 */
int main(int argc, char *argv[])
{
    std::string verify_plugin_path;
    std::string sample_path = "default_sample";
    std::vector<std::string> broker_socks;
    std::string plugin_path;
    std::string client_name = "algo_worker";
    std::string algorithm_id;
    int max_frames = -1;

    for (int i = 1; i < argc; ++i)
    {
        if (std::string(argv[i]) == "--verify-plugin" && i + 1 < argc)
        {
            verify_plugin_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--sample" && i + 1 < argc)
        {
            sample_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--plugin" && i + 1 < argc)
        {
            plugin_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--broker" && i + 1 < argc)
        {
            broker_socks.push_back(argv[++i]);
        }
        else if (std::string(argv[i]) == "--name" && i + 1 < argc)
        {
            client_name = argv[++i];
        }
        else if (std::string(argv[i]) == "--algorithm-id" && i + 1 < argc)
        {
            algorithm_id = argv[++i];
        }
        else if (std::string(argv[i]) == "--max-frames" && i + 1 < argc)
        {
            max_frames = std::stoi(argv[++i]);
        }
    }

    if (!verify_plugin_path.empty())
    {
        RunnerLogWriterGuard log_writer;
        if (!log_writer.started()) return 1;
        return zhulong::run_verify_plugin(verify_plugin_path, sample_path);
    }

    if (!plugin_path.empty() && !broker_socks.empty())
    {
        RunnerLogWriterGuard log_writer;
        if (!log_writer.started()) return 1;
        return zhulong::run_ipc_workers(plugin_path, broker_socks, client_name, max_frames, algorithm_id);
    }

    std::cout << "Zhulong Universal Native Algo Runner (migration shim, v1.0)" << std::endl;
    std::cout << "Usage: zhulong_algo_runner --verify-plugin <plugin.so> [--sample <path>]" << std::endl;
    std::cout << "   or: zhulong_algo_runner --plugin <plugin.so> --broker <sock_path> [--broker <sock_path_2> ...] [--name <name>] [--algorithm-id <id>] [--max-frames <N>]" << std::endl;
    return 0;
}
