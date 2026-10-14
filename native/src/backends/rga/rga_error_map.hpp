#pragma once

// RGA 私有错误映射：IM_STATUS → zhulong_status_t。
//
// ⚠️ 本文件是唯一允许 include RGA 头文件的头之一（另一个是 rga_types.hpp）。
// 它只被 native/src/backends/rga/ 内的实现引用，不得泄漏到 runtime/ 或 sdk/include/。
//
// ⚠️ 头文件包含顺序：RGA 的 im2d_single.h 使用了 NULL 但未包含 <cstddef>，
// 必须先包含 <cstddef>，否则交叉编译报 `'NULL' was not declared in this scope`。

#include <cstddef>

#include <rga/im2d.h>

#include <zhulong/plugin.h>

namespace zhulong {

// IM_STATUS（板端 im2d_type.h:277-284 实测）：
//   IM_STATUS_NOERROR       =  2
//   IM_STATUS_SUCCESS       =  1
//   IM_STATUS_NOT_SUPPORTED = -1
//   IM_STATUS_OUT_OF_MEMORY = -2
//   IM_STATUS_INVALID_PARAM = -3
//   IM_STATUS_ILLEGAL_PARAM = -4
//   IM_STATUS_ERROR_VERSION = -5
//   IM_STATUS_FAILED        =  0
inline zhulong_status_t map_rga_status(IM_STATUS status) {
    switch (status) {
        case IM_STATUS_SUCCESS:
        case IM_STATUS_NOERROR:
            return ZHULONG_PLUGIN_OK;
        case IM_STATUS_INVALID_PARAM:
        case IM_STATUS_ILLEGAL_PARAM:
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case IM_STATUS_NOT_SUPPORTED:
            // 版本不匹配对调用方而言等价于「这个环境不支持」→ NOT_SUPPORTED（触发 CPU 回退）。
        case IM_STATUS_ERROR_VERSION:
            return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        case IM_STATUS_OUT_OF_MEMORY:
            return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
        case IM_STATUS_FAILED:
        default:
            return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

} // namespace zhulong
