/**
 * @file mpp_loader.hpp
 * @brief Rockchip MPP 动态库装载器与硬件探针
 *
 * 通过 dlopen 动态加载 librockchip_mpp / libmpp，实现零构建期强依赖与跨平台安全隔离。
 */

#pragma once

#include "mpp_api_types.hpp"

#include <mutex>

namespace zhulong::mpp {

class MppLoader {
public:
    static MppLoader& instance();

    /**
     * @brief 检查当前系统是否支持 Rockchip MPP 硬件解码
     *
     * 探针条件：/dev/mpp_service 节点可访问且动态加载 librockchip_mpp 成功。
     */
    bool is_supported();

    // 导出的 MPP 核心函数指针
    int (*mpp_create)(MppCtx *ctx, MppApi **mpi){nullptr};
    int (*mpp_init)(MppCtx ctx, MppCtxType type, MppCodingType coding){nullptr};
    int (*mpp_destroy)(MppCtx ctx){nullptr};

    int (*mpp_packet_init)(MppPacket *packet, void *ptr, size_t size){nullptr};
    int (*mpp_packet_deinit)(MppPacket *packet){nullptr};
    void (*mpp_packet_set_pts)(MppPacket packet, int64_t pts){nullptr};
    int (*mpp_packet_set_eos)(MppPacket packet){nullptr};

    int (*mpp_frame_init)(MppFrame *frame){nullptr};
    int (*mpp_frame_deinit)(MppFrame *frame){nullptr};
    uint32_t (*mpp_frame_get_width)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_height)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_hor_stride)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_ver_stride)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_errinfo)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_discard)(const MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_eos)(const MppFrame frame){nullptr};
    MppFrameFormat (*mpp_frame_get_fmt)(MppFrame frame){nullptr};
    uint32_t (*mpp_frame_get_info_change)(const MppFrame frame){nullptr};
    int64_t (*mpp_frame_get_pts)(const MppFrame frame){nullptr};
    MppBuffer (*mpp_frame_get_buffer)(const MppFrame frame){nullptr};
    size_t (*mpp_frame_get_buf_size)(const MppFrame frame){nullptr};

    int (*mpp_buffer_group_get)(MppBufferGroup *group, MppBufferType type, MppBufferMode mode,
                                const char *tag, const char *caller){nullptr};
    int (*mpp_buffer_group_put)(MppBufferGroup group){nullptr};
    int (*mpp_buffer_group_clear)(MppBufferGroup group){nullptr};
    int (*mpp_buffer_group_limit_config)(MppBufferGroup group, size_t size, int32_t count){nullptr};

    int (*mpp_buffer_inc_ref_with_caller)(MppBuffer buffer, const char *caller){nullptr};
    int (*mpp_buffer_put_with_caller)(MppBuffer buffer, const char *caller){nullptr};
    int (*mpp_buffer_get_fd_with_caller)(MppBuffer buffer, const char *caller){nullptr};

    // 辅助便捷方法
    int buffer_inc_ref(MppBuffer buf) const noexcept {
        return mpp_buffer_inc_ref_with_caller ? mpp_buffer_inc_ref_with_caller(buf, "ZhulongMpp") : -1;
    }

    int buffer_put(MppBuffer buf) const noexcept {
        return mpp_buffer_put_with_caller ? mpp_buffer_put_with_caller(buf, "ZhulongMpp") : -1;
    }

    int buffer_get_fd(MppBuffer buf) const noexcept {
        return mpp_buffer_get_fd_with_caller ? mpp_buffer_get_fd_with_caller(buf, "ZhulongMpp") : -1;
    }

private:
    MppLoader() = default;
    ~MppLoader();

    MppLoader(const MppLoader&) = delete;
    MppLoader& operator=(const MppLoader&) = delete;

    void load_symbols();

    void *lib_handle_{nullptr};
    bool is_supported_{false};
    bool loaded_{false};
    std::mutex mutex_;
};

} // namespace zhulong::mpp
