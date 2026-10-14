#pragma once

// RKNN 私有资源模型：ModelAsset（根上下文 + 权重）与 SessionContext（dup 派生上下文）。
//
// ⚠️ 本文件是唯一允许 include <rknn_api.h> 的头文件之一（另一个是 rknn_error_map.hpp）。
// 它只被 native/src/backends/rknn/ 内的实现引用，不得泄漏到 runtime/ 或 sdk/include/。

#include <rknn_api.h>

#include <algorithm/runtime/inference_backend.hpp>

#include <mutex>
#include <string>
#include <vector>

namespace zhulong {

// 每包 / 每模型一个，进程内共享；持有根上下文（权重在此加载一次）。
struct RknnModelAsset : public ModelAsset {
    rknn_context root_ctx{0};
    bool root_valid{false};

    // 串行化 rknn_dup_context —— 不假定复制接口可并发调用。
    std::mutex dup_mutex;

    std::string model_path;
    std::string model_sha256;

    rknn_input_output_num io_num{};
    std::vector<rknn_tensor_attr> input_attrs;
    std::vector<rknn_tensor_attr> output_attrs;
    rknn_mem_size mem_size{};
    rknn_sdk_version sdk_version{};

    ~RknnModelAsset() override {
        if (root_valid) {
            rknn_destroy(root_ctx);
            root_valid = false;
        }
    }
};

// 每 worker 一个，独占；上下文由 rknn_dup_context(&root, &ctx) 派生。
struct RknnSessionContext : public SessionContext {
    rknn_context ctx{0};
    bool valid{false};

    // ⚠️ 持有 asset 的 shared_ptr，保证 session 一定早于 root 销毁（R5 的关键）。
    std::shared_ptr<RknnModelAsset> model;

    ~RknnSessionContext() override {
        if (valid) {
            rknn_destroy(ctx);
            valid = false;
        }
    }
};

} // namespace zhulong
