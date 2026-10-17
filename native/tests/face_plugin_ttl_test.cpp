/**
 * @file face_plugin_ttl_test.cpp
 * @brief 宿主 FacePluginLoader 动态加载、Fail-Closed 报错与 TTL 空闲自动卸载黑盒测试
 *
 * 验证链路（全程只使用 C ABI，不触碰任何内部实现细节）：
 * 1. 首次提取按 ZHULONG_FACE_PLUGIN_PATH 懒加载插件替身并返回 512 维单位向量；
 * 2. 抽走插件文件后空闲超过 ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC，
 *    Reaper 必须调用 zhulong_face_extract_release 并 dlclose（由标记文件断言）；
 * 3. TTL 之后的提取必须因重新 dlopen 失败而显式返回 Zhulong_ERR_UNSUPPORTED，
 *    证明缓存句柄已被真正丢弃，而不是「看起来还在」；
 * 4. 恢复插件文件后，下一次请求必须能够重新唤醒加载并正常提取。
 */

#include "Zhulong/engine.h"

#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

#include <sys/stat.h>

namespace {

constexpr int32_t kIdleTimeoutSec = 1;
constexpr uint32_t kFeatureDim = 512;

int fail(const char *step, int code) {
    std::cerr << "[face_plugin_ttl] FAILED at " << step << " (code=" << code << ")" << std::endl;
    return code;
}

/** @brief 逐字节复制文件（用于在临时目录中安置/移除插件替身） */
bool copy_file(const std::string &from, const std::string &to) {
    std::ifstream src(from, std::ios::binary);
    if (!src.is_open()) {
        return false;
    }
    std::ofstream dst(to, std::ios::binary | std::ios::trunc);
    if (!dst.is_open()) {
        return false;
    }
    dst << src.rdbuf();
    return dst.good();
}

/** @brief 统计标记文件中某事件出现的次数 */
int count_events(const std::string &marker, const std::string &event) {
    std::ifstream in(marker);
    int count = 0;
    std::string line;
    while (std::getline(in, line)) {
        if (line == event) {
            ++count;
        }
    }
    return count;
}

/** @brief 断言一次成功提取：状态 OK、维度 512、L2 单位模长 */
int expect_success(Zhulong_engine_h engine, const char *step) {
    std::vector<uint8_t> pixels(64 * 64 * 3, 0x7F);
    std::vector<float> features(kFeatureDim, 0.0f);
    uint32_t dim = kFeatureDim;

    Zhulong_status_t st = Zhulong_engine_extract_face_feature(
        engine, pixels.data(), 64, 64, 64 * 3, 2, features.data(), &dim);
    if (st != Zhulong_OK) {
        return fail(step, 10);
    }
    if (dim != kFeatureDim) {
        return fail(step, 11);
    }

    float norm_sq = 0.0f;
    for (float value : features) {
        norm_sq += value * value;
    }
    if (std::abs(std::sqrt(norm_sq) - 1.0f) > 1e-4f) {
        return fail(step, 12);
    }
    return 0;
}

} // namespace

int main(int argc, char *argv[]) {
    if (argc < 3) {
        std::cerr << "usage: ZhulongFacePluginTtlTests <mock_plugin.so> <scratch_dir>" << std::endl;
        return 1;
    }
    const std::string source_plugin = argv[1];
    const std::string scratch_dir = argv[2];
    const std::string active_plugin = scratch_dir + "/libMockFaceExtractPlugin.so";
    const std::string marker = scratch_dir + "/events.log";

    ::mkdir(scratch_dir.c_str(), 0755);
    std::remove(active_plugin.c_str());
    std::remove(marker.c_str());
    if (!copy_file(source_plugin, active_plugin)) {
        return fail("stage mock plugin", 2);
    }

    // 环境变量必须在 FacePluginLoader 单例构造（首次引擎创建或提取）之前注入
    setenv("ZHULONG_FACE_PLUGIN_PATH", active_plugin.c_str(), 1);
    setenv("ZHULONG_MOCK_PLUGIN_MARKER", marker.c_str(), 1);
    setenv("ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC", "1", 1);

    Zhulong_engine_h engine = nullptr;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == nullptr) {
        return fail("engine create", 3);
    }

    // 1. 首次提取：懒加载插件替身并返回其写入的 512 维单位向量
    if (int rc = expect_success(engine, "initial extract"); rc != 0) {
        return rc;
    }
    if (count_events(marker, "extract") != 1) {
        return fail("initial extract recorded", 4);
    }

    // 2. 抽走插件文件：若宿主仍缓存着已加载句柄，后续提取依然会成功
    std::remove(active_plugin.c_str());

    // 3. 空闲等待超过 TTL，触发 Reaper 自动 release + dlclose
    std::this_thread::sleep_for(std::chrono::seconds(kIdleTimeoutSec + 1));

    // 4. TTL 后再次提取：必须因重新 dlopen 失败而闭环报错，证明句柄已被真正卸载
    {
        std::vector<uint8_t> pixels(64 * 64 * 3, 0x7F);
        std::vector<float> features(kFeatureDim, 0.0f);
        uint32_t dim = kFeatureDim;
        Zhulong_status_t st = Zhulong_engine_extract_face_feature(
            engine, pixels.data(), 64, 64, 64 * 3, 2, features.data(), &dim);
        if (st != Zhulong_ERR_UNSUPPORTED) {
            return fail("post-TTL fail-closed", 5);
        }
    }
    if (count_events(marker, "release") < 1) {
        return fail("reaper release invoked", 6);
    }

    // 5. 恢复插件文件：下一次请求必须能重新唤醒加载并正常提取
    if (!copy_file(source_plugin, active_plugin)) {
        return fail("restore mock plugin", 7);
    }
    if (int rc = expect_success(engine, "re-wake extract"); rc != 0) {
        return rc;
    }

    // 6. 销毁引擎：汇合 Reaper 线程并卸载，无线程或句柄悬挂
    Zhulong_engine_destroy(engine);

    std::cout << "[face_plugin_ttl] all checks passed" << std::endl;
    return 0;
}
