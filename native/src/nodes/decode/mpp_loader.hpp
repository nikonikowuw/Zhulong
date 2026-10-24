/**
 * @file mpp_loader.hpp
 * @brief Rockchip MPP 动态库装载器与硬件探针
 *
 * 通过 dlopen 动态加载 librockchip_mpp / libmpp，实现零构建期强依赖与跨平台安全隔离。
 */

#pragma once

#include "nodes/decode/mpp_api_types.hpp"

#include <mutex>

namespace zhulong::mpp {

class MppLoader {
public:
    /**
     * @brief MPP 符号表
     *
     * 加载完成后成员指针不再变更，外部只能通过 `MppLoader::instance().api()` 只读访问，
     * 避免函数表被业务代码误改写。
     */
    struct Api {
        int (*create)(MppCtx *ctx, MppApi **mpi){nullptr};
        int (*init)(MppCtx ctx, MppCtxType type, MppCodingType coding){nullptr};
        int (*destroy)(MppCtx ctx){nullptr};

        int (*packet_init)(MppPacket *packet, void *ptr, size_t size){nullptr};
        int (*packet_deinit)(MppPacket *packet){nullptr};
        void (*packet_set_pts)(MppPacket packet, int64_t pts){nullptr};
        int (*packet_set_eos)(MppPacket packet){nullptr};

        int (*frame_init)(MppFrame *frame){nullptr};
        int (*frame_deinit)(MppFrame *frame){nullptr};
        uint32_t (*frame_get_width)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_height)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_hor_stride)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_ver_stride)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_errinfo)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_discard)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_eos)(const MppFrame frame){nullptr};
        MppFrameFormat (*frame_get_fmt)(const MppFrame frame){nullptr};
        uint32_t (*frame_get_info_change)(const MppFrame frame){nullptr};
        int64_t (*frame_get_pts)(const MppFrame frame){nullptr};
        MppBuffer (*frame_get_buffer)(const MppFrame frame){nullptr};
        size_t (*frame_get_buf_size)(const MppFrame frame){nullptr};

        int (*buffer_group_get)(MppBufferGroup *group, MppBufferType type, MppBufferMode mode,
                                const char *tag, const char *caller){nullptr};
        int (*buffer_group_put)(MppBufferGroup group){nullptr};
        int (*buffer_group_clear)(MppBufferGroup group){nullptr};
        int (*buffer_group_limit_config)(MppBufferGroup group, size_t size, int32_t count){nullptr};

        int (*buffer_inc_ref)(MppBuffer buffer, const char *caller){nullptr};
        int (*buffer_put)(MppBuffer buffer, const char *caller){nullptr};
        int (*buffer_get_fd)(MppBuffer buffer, const char *caller){nullptr};
    };

    static MppLoader &instance();

    /**
     * @brief 检查当前系统是否支持 Rockchip MPP 硬件解码
     *
     * 探针条件：/dev/mpp_service 节点可访问且动态加载 librockchip_mpp 成功、关键符号齐全。
     * 首次调用执行加载，失败后不再重试（fail closed）。
     */
    bool is_supported();

    /** @brief 只读符号表；仅在 is_supported() 返回 true 后有效 */
    const Api &api() const noexcept { return api_; }

    // 辅助便捷方法（函数表缺失时返回 -1，绝不空指针解引用）
    int inc_ref(MppBuffer buf) const noexcept {
        return api_.buffer_inc_ref ? api_.buffer_inc_ref(buf, "ZhulongMpp") : -1;
    }

    int put(MppBuffer buf) const noexcept {
        return api_.buffer_put ? api_.buffer_put(buf, "ZhulongMpp") : -1;
    }

    int fd(MppBuffer buf) const noexcept {
        return api_.buffer_get_fd ? api_.buffer_get_fd(buf, "ZhulongMpp") : -1;
    }

private:
    MppLoader() = default;
    ~MppLoader();

    MppLoader(const MppLoader &) = delete;
    MppLoader &operator=(const MppLoader &) = delete;

    void load_symbols();

    void *lib_handle_{nullptr};
    Api api_{};
    bool is_supported_{false};
    bool loaded_{false};
    std::mutex mutex_;
};

} // namespace zhulong::mpp
