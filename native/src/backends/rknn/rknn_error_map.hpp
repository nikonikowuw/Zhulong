/**
 * @file rknn_error_map.hpp
 * @brief RKNN 返回码 → zhulong_status_t 统一错误映射
 *
 * ⚠️ 本文件为 header-only（inline）。相对 design.md §2 的
 * `rknn_error_map.hpp/.cpp` 拆分为两个文件，这里合并为单头文件，
 * 理由是错误映射是纯整数映射，header-only 才能让测试目标直接 include
 * 而不必链接 runtime .so（设计文档该节只列了文件名，未规定必须分成两个文件）。
 *
 * 本文件是唯一允许 include <rknn_api.h> 的头文件之一（另一个是 rknn_types.hpp）。
 *
 * ⚠️ 映射口径关键区分：设备不可用等环境类错误映射为 NOT_SUPPORTED，
 * 而 **不是** INTERNAL —— 便于上层区分「环境问题」与「代码 bug」。
 */

#pragma once

#include <rknn_api.h>
#include <zhulong/plugin.h>

namespace zhulong {

/**
 * @brief 将 RKNN 返回码映射为统一状态码
 *
 * @param ret RKNN 返回码（RKNN_SUCC / RKNN_ERR_*）
 * @return zhulong_status_t 统一状态码（未知值归为 INTERNAL）
 */
inline zhulong_status_t map_rknn_error(int ret) {
    switch (ret) {
        case RKNN_SUCC:
            return ZHULONG_PLUGIN_OK;
        case RKNN_ERR_PARAM_INVALID:
        case RKNN_ERR_MODEL_INVALID:
        case RKNN_ERR_CTX_INVALID:
        case RKNN_ERR_INPUT_INVALID:
        case RKNN_ERR_OUTPUT_INVALID:
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case RKNN_ERR_DEVICE_UNAVAILABLE:
        case RKNN_ERR_DEVICE_UNMATCH:
        case RKNN_ERR_TARGET_PLATFORM_UNMATCH:
        case RKNN_ERR_INCOMPATILE_PRE_COMPILE_MODEL:
        case RKNN_ERR_INCOMPATILE_OPTIMIZATION_LEVEL_VERSION:
            return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        case RKNN_ERR_MALLOC_FAIL:
            return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
        case RKNN_ERR_TIMEOUT:
            return ZHULONG_PLUGIN_ERR_TIMEOUT;
        case RKNN_ERR_FAIL:
        default:
            return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

} // namespace zhulong
