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

    // 1. 硬件节点探针：/dev/mpp_service 设备节点不存在则直接判定不支持
    if (access("/dev/mpp_service", F_OK) != 0) {
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

    // 3. 解析核心生命周期符号
    bool ok = true;
    ok &= load_sym(lib_handle_, "mpp_create", mpp_create);
    ok &= load_sym(lib_handle_, "mpp_init", mpp_init);
    ok &= load_sym(lib_handle_, "mpp_destroy", mpp_destroy);

    // 4. 解析 Packet 与 Frame 操作符号
    ok &= load_sym(lib_handle_, "mpp_packet_init", mpp_packet_init);
    ok &= load_sym(lib_handle_, "mpp_packet_deinit", mpp_packet_deinit);
    ok &= load_sym(lib_handle_, "mpp_packet_set_pts", mpp_packet_set_pts);
    ok &= load_sym(lib_handle_, "mpp_packet_set_eos", mpp_packet_set_eos);

    ok &= load_sym(lib_handle_, "mpp_frame_init", mpp_frame_init);
    ok &= load_sym(lib_handle_, "mpp_frame_deinit", mpp_frame_deinit);
    ok &= load_sym(lib_handle_, "mpp_frame_get_width", mpp_frame_get_width);
    ok &= load_sym(lib_handle_, "mpp_frame_get_height", mpp_frame_get_height);
    ok &= load_sym(lib_handle_, "mpp_frame_get_hor_stride", mpp_frame_get_hor_stride);
    ok &= load_sym(lib_handle_, "mpp_frame_get_ver_stride", mpp_frame_get_ver_stride);
    ok &= load_sym(lib_handle_, "mpp_frame_get_errinfo", mpp_frame_get_errinfo);
    ok &= load_sym(lib_handle_, "mpp_frame_get_discard", mpp_frame_get_discard);
    ok &= load_sym(lib_handle_, "mpp_frame_get_eos", mpp_frame_get_eos);
    ok &= load_sym(lib_handle_, "mpp_frame_get_fmt", mpp_frame_get_fmt);
    ok &= load_sym(lib_handle_, "mpp_frame_get_info_change", mpp_frame_get_info_change);
    ok &= load_sym(lib_handle_, "mpp_frame_get_pts", mpp_frame_get_pts);
    ok &= load_sym(lib_handle_, "mpp_frame_get_buffer", mpp_frame_get_buffer);
    ok &= load_sym(lib_handle_, "mpp_frame_get_buf_size", mpp_frame_get_buf_size);

    // 5. 解析 Buffer 与 BufferGroup 符号
    ok &= load_sym(lib_handle_, "mpp_buffer_group_get", mpp_buffer_group_get);
    ok &= load_sym(lib_handle_, "mpp_buffer_group_put", mpp_buffer_group_put);
    ok &= load_sym(lib_handle_, "mpp_buffer_group_clear", mpp_buffer_group_clear);
    ok &= load_sym(lib_handle_, "mpp_buffer_group_limit_config", mpp_buffer_group_limit_config);

    ok &= load_sym(lib_handle_, "mpp_buffer_inc_ref_with_caller", mpp_buffer_inc_ref_with_caller);
    ok &= load_sym(lib_handle_, "mpp_buffer_put_with_caller", mpp_buffer_put_with_caller);
    ok &= load_sym(lib_handle_, "mpp_buffer_get_fd_with_caller", mpp_buffer_get_fd_with_caller);

    if (ok) {
        is_supported_ = true;
    } else {
        dlclose(lib_handle_);
        lib_handle_ = nullptr;
    }
}

} // namespace zhulong::mpp
