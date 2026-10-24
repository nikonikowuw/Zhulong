/**
 * @file mpp_loader.cpp
 * @brief Rockchip MPP 动态库装载器与硬件探针实现
 */

#include "nodes/decode/mpp_loader.hpp"

#include <dlfcn.h>
#include <unistd.h>

namespace zhulong::mpp {

MppLoader &MppLoader::instance() {
    static MppLoader loader;
    return loader;
}

MppLoader::~MppLoader() {
    if (lib_handle_) {
        dlclose(lib_handle_);
        lib_handle_ = nullptr;
    }
}

bool MppLoader::is_supported() {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!loaded_) {
        load_symbols();
        loaded_ = true;
    }
    return is_supported_;
}

template <typename Fn>
static bool load_sym(void *handle, const char *name, Fn &out_fn) {
    out_fn = reinterpret_cast<Fn>(dlsym(handle, name));
    return (out_fn != nullptr);
}

void MppLoader::load_symbols() {
    is_supported_ = false;

    // 1. 硬件节点探针：/dev/mpp_service 设备节点不存在或当前用户无读写权限则直接判定不支持
    if (access("/dev/mpp_service", R_OK | W_OK) != 0) {
        return;
    }

    // 2. 搜索并加载 MPP 动态共享库
    static const char *kLibNames[] = {
        "librockchip_mpp.so.1",
        "librockchip_mpp.so",
        "librockchip_mpp.so.0",
        "libmpp.so.1",
        "libmpp.so",
        nullptr
    };

    for (int i = 0; kLibNames[i] != nullptr; ++i) {
        lib_handle_ = dlopen(kLibNames[i], RTLD_NOW | RTLD_LOCAL);
        if (lib_handle_) {
            break;
        }
    }

    if (!lib_handle_) {
        return;
    }

    // 3. 解析核心生命周期、数据包/帧处理以及缓冲池符号。
    // 使用独立布尔累加（而非 `ok && load_sym(...)` 短路），确保每个符号都被真实探测一次，
    // 缺失项在日志中可全量定位。
    bool ok = true;
#define ZL_MPP_SYM(field, symbol) ok = load_sym(lib_handle_, symbol, api_.field) && ok

    ZL_MPP_SYM(create, "mpp_create");
    ZL_MPP_SYM(init, "mpp_init");
    ZL_MPP_SYM(destroy, "mpp_destroy");

    ZL_MPP_SYM(packet_init, "mpp_packet_init");
    ZL_MPP_SYM(packet_deinit, "mpp_packet_deinit");
    ZL_MPP_SYM(packet_set_pts, "mpp_packet_set_pts");
    ZL_MPP_SYM(packet_set_eos, "mpp_packet_set_eos");

    ZL_MPP_SYM(frame_init, "mpp_frame_init");
    ZL_MPP_SYM(frame_deinit, "mpp_frame_deinit");
    ZL_MPP_SYM(frame_get_width, "mpp_frame_get_width");
    ZL_MPP_SYM(frame_get_height, "mpp_frame_get_height");
    ZL_MPP_SYM(frame_get_hor_stride, "mpp_frame_get_hor_stride");
    ZL_MPP_SYM(frame_get_ver_stride, "mpp_frame_get_ver_stride");
    ZL_MPP_SYM(frame_get_errinfo, "mpp_frame_get_errinfo");
    ZL_MPP_SYM(frame_get_discard, "mpp_frame_get_discard");
    ZL_MPP_SYM(frame_get_eos, "mpp_frame_get_eos");
    ZL_MPP_SYM(frame_get_fmt, "mpp_frame_get_fmt");
    ZL_MPP_SYM(frame_get_info_change, "mpp_frame_get_info_change");
    ZL_MPP_SYM(frame_get_pts, "mpp_frame_get_pts");
    ZL_MPP_SYM(frame_get_buffer, "mpp_frame_get_buffer");
    ZL_MPP_SYM(frame_get_buf_size, "mpp_frame_get_buf_size");

    ZL_MPP_SYM(buffer_group_get, "mpp_buffer_group_get");
    ZL_MPP_SYM(buffer_group_put, "mpp_buffer_group_put");
    ZL_MPP_SYM(buffer_group_clear, "mpp_buffer_group_clear");
    ZL_MPP_SYM(buffer_group_limit_config, "mpp_buffer_group_limit_config");

    ZL_MPP_SYM(buffer_inc_ref, "mpp_buffer_inc_ref_with_caller");
    ZL_MPP_SYM(buffer_put, "mpp_buffer_put_with_caller");
    ZL_MPP_SYM(buffer_get_fd, "mpp_buffer_get_fd_with_caller");
#undef ZL_MPP_SYM

    if (ok) {
        is_supported_ = true;
    } else {
        api_ = Api{};
        dlclose(lib_handle_);
        lib_handle_ = nullptr;
    }
}

} // namespace zhulong::mpp
