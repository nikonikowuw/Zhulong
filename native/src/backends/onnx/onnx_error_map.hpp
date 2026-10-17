/**
 * @file onnx_error_map.hpp
 * @brief ONNX Runtime 错误码与数据类型 → Zhulong 统一契约映射
 *
 * ⚠️ 本文件为 header-only（inline），仅允许在 native/src/backends/onnx/ 及对应后端单元测试中包含，
 * 严禁泄露至公开 SDK 头文件。
 */

#pragma once

#include <onnxruntime_c_api.h>
#include <zhulong/plugin.h>
#include <algorithm/runtime/tensor.hpp>

namespace zhulong {

/**
 * @brief 将 ONNX Runtime 的 OrtErrorCode 映射为统一的 zhulong_status_t
 *
 * @param code ORT 错误码枚举
 * @return zhulong_status_t 统一插件状态码
 */
inline zhulong_status_t map_onnx_error(OrtErrorCode code) {
    switch (code) {
        case ORT_OK:
            return ZHULONG_PLUGIN_OK;
        case ORT_INVALID_ARGUMENT:
        case ORT_NO_MODEL:
        case ORT_INVALID_PROTOBUF:
        case ORT_INVALID_GRAPH:
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case ORT_NO_SUCHFILE:
            return ZHULONG_PLUGIN_ERR_NOT_FOUND;
        case ORT_MODEL_LOADED:
            return ZHULONG_PLUGIN_ERR_ALREADY_EXISTS;
        case ORT_NOT_IMPLEMENTED:
        case ORT_EP_FAIL:
            return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        case ORT_ENGINE_ERROR:
        case ORT_RUNTIME_EXCEPTION:
        case ORT_FAIL:
        default:
            return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

/**
 * @brief 将 ONNX 张量元素类型映射为 zhulong::DataType
 *
 * @param onnx_type ORT 张量元素类型
 * @param[out] out_dtype 转换后的 zhulong::DataType
 * @return bool 支持该类型返回 true，不支持返回 false
 */
inline bool map_onnx_tensor_type(ONNXTensorElementDataType onnx_type, DataType& out_dtype) {
    switch (onnx_type) {
        case ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT:
            out_dtype = DataType::FLOAT32;
            return true;
        case ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT8:
            out_dtype = DataType::UINT8;
            return true;
        case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT8:
            out_dtype = DataType::INT8;
            return true;
        case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT32:
            out_dtype = DataType::INT32;
            return true;
        case ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64:
            out_dtype = DataType::INT64;
            return true;
        default:
            return false;
    }
}

} // namespace zhulong
