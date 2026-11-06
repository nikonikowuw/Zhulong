/**
 * @file rknn_types.hpp
 * @brief RKNN 私有资源模型：根上下文资产与 dup 派生会话上下文
 *
 * ⚠️ 本文件是唯一允许 include <rknn_api.h> 的头文件之一（另一个是 rknn_error_map.hpp）。
 * 它只被 native/src/backends/rknn/ 内的实现引用，不得泄露到 runtime/ 或 sdk/include/
 * （directory-structure.md 铁律 4：厂商 SDK 物理隔离）。
 *
 * 核心设计原则（资源生命周期，R.1/R.5/R.11）：
 * 1. RknnModelAsset 每模型一个、进程内共享，持有唯一根上下文（权重仅加载一次）。
 * 2. RknnSessionContext 每 Worker 一个、独占，上下文由 rknn_dup_context 派生。
 * 3. ⚠️ 失败路径不得 double-destroy：root_valid 只在全部查询成功后置 true，
 *    所有提前返回点的 rknn_destroy(root) 都在此之前，因此局部 asset 析构时
 *    root_valid == false，不会二次销毁。
 * 4. ⚠️ session 通过 shared_ptr 持有 asset，保证 session 一定早于 root 销毁。
 * 5. ⚠️ DMA-BUF 注册句柄（rknn_tensor_mem）由带上下文的自定义 deleter 独占持有，
 *    析构即释放：不再需要手工 `reset(context)`，也不会因新增使用点而漏释放。
 */

#pragma once

#include <rknn_api.h>

#include <zhulong/runtime/inference_backend.hpp>

#include <memory>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

namespace zhulong {

// 每包 / 每模型一个，进程内共享；持有根上下文（权重在此加载一次）。
//
// ⚠️ 权重共享实测结论（RK3568 真机，同一模型 4 个上下文）：
// 4 × 独立 rknn_init 峰值增量 +50.4 MB，1 × rknn_init + 4 × rknn_dup_context
// 仅 +14.3 MB（收益约 3.5×），且两组输出逐元素一致。
// ⚠️ RKNN_QUERY_MEM_SIZE 的逻辑值（两组 total_weight_size 均为 9348096）
// **不能当物理占用**：真实差异只能由 RSS/PSS 体现。
struct RknnModelAsset : public ModelAsset {
    rknn_context root_ctx{0};  /**< 根上下文（唯一权重持有者） */
    bool root_valid{false};    /**< ⚠️ 仅在全部查询成功后置 true，用于失败路径防 double-destroy */

    // 串行化 rknn_dup_context —— 不假定复制接口可并发调用。
    std::mutex dup_mutex;      /**< ⚠️ 串行化 rknn_dup_context 的互斥锁 */

    std::string model_path;    /**< 模型文件路径 */
    std::string model_sha256;  /**< 模型内容摘要（缓存身份） */

    rknn_input_output_num io_num{};                /**< 输入/输出张量数量（从 Runtime 查询，不猜） */
    std::vector<rknn_tensor_attr> input_attrs;     /**< 输入张量属性（从 Runtime 查询） */
    std::vector<rknn_tensor_attr> output_attrs;    /**< 输出张量属性（从 Runtime 查询） */
    bool is_dynamic{false};                        /**< 是否为动态 shape 模型 */
    std::vector<rknn_input_range> dynamic_ranges;  /**< 动态输入 shape 范围（从 Runtime 查询） */
    rknn_mem_size mem_size{};                      /**< ⚠️ 必须由独立的一次性上下文查询（见 .cpp 的 R10 说明） */
    rknn_sdk_version sdk_version{};                /**< 运行库版本（证据记录） */

    RknnModelAsset() = default;
    RknnModelAsset(const RknnModelAsset&) = delete;
    RknnModelAsset& operator=(const RknnModelAsset&) = delete;
    RknnModelAsset(RknnModelAsset&&) = delete;
    RknnModelAsset& operator=(RknnModelAsset&&) = delete;

    ~RknnModelAsset() override {
        if (root_valid) {
            rknn_destroy(root_ctx);
            root_valid = false;
        }
    }
};

/**
 * @brief rknn_tensor_mem 的释放器：把上下文与句柄绑在一起，析构即 rknn_destroy_mem
 *
 * ⚠️ 为什么不是「手工 reset(context)」：外部注册句柄必须由创立它的上下文释放，
 *    靠调用点传参就只能靠人来保证，任何新增使用点漏一次就是 N 帧的显存泄漏。
 */
struct RknnTensorMemDeleter {
    rknn_context ctx{0};

    void operator()(rknn_tensor_mem* mem) const noexcept {
        if (mem != nullptr && ctx != 0) {
            rknn_destroy_mem(ctx, mem);
        }
    }
};

/** @brief 独占所有权的 rknn_tensor_mem 句柄 */
using RknnTensorMemPtr = std::unique_ptr<rknn_tensor_mem, RknnTensorMemDeleter>;

// 缓存已注册的外部 DMA-BUF 内存映射，避免热路径每帧调用 rknn_create_mem_from_fd。
// 值语义 + Rule of Zero：句柄随本结构析构自动归还，向量清空即全部释放。
struct CachedDmaMem {
    int fd{-1};
    void* va{nullptr};
    size_t size{0};
    DataType dtype{DataType::UINT8};
    RknnTensorMemPtr mem{nullptr, RknnTensorMemDeleter{}};

    /**
     * @brief 内存身份一致可复用注册；绑定 dtype 另行检查，shape 切换由会话清空缓存。
     */
    [[nodiscard]] bool matches(int candidate_fd, const void* candidate_va, size_t candidate_size) const noexcept {
        return mem != nullptr && fd == candidate_fd && va == candidate_va && size == candidate_size;
    }

    /**
     * @brief 释放当前注册并清空凭据
     */
    void reset() noexcept {
        mem.reset();
        fd = -1;
        va = nullptr;
        size = 0;
    }

    /**
     * @brief 事务提交：仅在 rknn_set_io_mem 成功之后调用
     */
    void commit(RknnTensorMemPtr next_mem, int next_fd, void* next_va, size_t next_size,
                DataType next_dtype) noexcept {
        mem = std::move(next_mem);
        fd = next_fd;
        va = next_va;
        size = next_size;
        dtype = next_dtype;
    }
};

// 每 worker 一个，独占；上下文由 rknn_dup_context(&root, &ctx) 派生。
//
// ⚠️ 单 NPU 核（RK3568）：不得声称吞吐随 worker 数线性扩展。真机实测 1/2/4 worker
// 各 20 次推理耗时 316.8ms / 374.1ms / 588.7ms。收益方向是 CPU 前处理/后处理与
// NPU 执行的**重叠**，不是 NPU 并行；本实现不启用 RKNN_FLAG_ASYNC_MASK。
struct RknnSessionContext : public SessionContext {
    rknn_context ctx{0};   /**< 该 Worker 独占的派生上下文 */
    bool valid{false};     /**< 上下文是否有效（析构时据此决定是否 rknn_destroy） */

    // ⚠️ 持有 asset 的 shared_ptr，保证 session 一定早于 root 销毁（R5 的关键）。
    std::shared_ptr<RknnModelAsset> model;

    std::vector<CachedDmaMem> cached_input_mems;

    bool shapes_set{false};                                      /**< 当前上下文是否已配置 dynamic shape */
    std::vector<std::vector<int64_t>> current_input_shapes;      /**< 当前生效的输入 shape */
    std::vector<rknn_tensor_attr> current_input_attrs;             /**< 会话当前输入属性，不改共享 asset */
    std::vector<rknn_tensor_attr> current_output_attrs;          /**< 当前生效的输出属性 */

    RknnSessionContext() = default;
    RknnSessionContext(const RknnSessionContext&) = delete;
    RknnSessionContext& operator=(const RknnSessionContext&) = delete;
    RknnSessionContext(RknnSessionContext&&) = delete;
    RknnSessionContext& operator=(RknnSessionContext&&) = delete;

    ~RknnSessionContext() override {
        // ⚠️ 注册句柄必须在本上下文销毁之前归还（deleter 内部用 ctx）。
        cached_input_mems.clear();
        if (valid && ctx != 0) {
            rknn_destroy(ctx);
            valid = false;
        }
    }
};

} // namespace zhulong

