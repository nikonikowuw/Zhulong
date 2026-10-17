/**
 * @file mpp_loader.cpp
 * @brief Rockchip MPP 动态库装载器与硬件探针实现
 */

#include "mpp_loader.hpp"

#include <dlfcn.h>
#include <unistd.h>

namespace zhulong::mpp {

MppLoader& MppLoader::instance() {
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

template <typename T>
static bool load_sym(void *handle, const char *name, T &out_fn) {
    out_fn = reinterpret_cast<T>(dlsym(handle, name));
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

    // 3. 解析核心生命周期、数据包/帧处理以及缓冲池符号
    bool ok = true;
#define LOAD_SYM(sym) ok = ok && load_sym(lib_handle_, #sym, sym)

    LOAD_SYM(mpp_create);
    LOAD_SYM(mpp_init);
    LOAD_SYM(mpp_destroy);

    LOAD_SYM(mpp_packet_init);
    LOAD_SYM(mpp_packet_deinit);
    LOAD_SYM(mpp_packet_set_pts);
    LOAD_SYM(mpp_packet_set_eos);

    LOAD_SYM(mpp_frame_init);
    LOAD_SYM(mpp_frame_deinit);
    LOAD_SYM(mpp_frame_get_width);
    LOAD_SYM(mpp_frame_get_height);
    LOAD_SYM(mpp_frame_get_hor_stride);
    LOAD_SYM(mpp_frame_get_ver_stride);
    LOAD_SYM(mpp_frame_get_errinfo);
    LOAD_SYM(mpp_frame_get_discard);
    LOAD_SYM(mpp_frame_get_eos);
    LOAD_SYM(mpp_frame_get_fmt);
    LOAD_SYM(mpp_frame_get_info_change);
    LOAD_SYM(mpp_frame_get_pts);
    LOAD_SYM(mpp_frame_get_buffer);
    LOAD_SYM(mpp_frame_get_buf_size);

    LOAD_SYM(mpp_buffer_group_get);
    LOAD_SYM(mpp_buffer_group_put);
    LOAD_SYM(mpp_buffer_group_clear);
    LOAD_SYM(mpp_buffer_group_limit_config);

    LOAD_SYM(mpp_buffer_inc_ref_with_caller);
    LOAD_SYM(mpp_buffer_put_with_caller);
    LOAD_SYM(mpp_buffer_get_fd_with_caller);
#undef LOAD_SYM

    if (ok) {
        is_supported_ = true;
    } else {
        dlclose(lib_handle_);
        lib_handle_ = nullptr;
    }
}

} // namespace zhulong::mpp
