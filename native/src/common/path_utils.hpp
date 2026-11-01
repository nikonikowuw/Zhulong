/**
 * @file path_utils.hpp
 * @brief 路径处理小工具（供后端/节点共用，避免同一语义出现两份实现）
 *
 * 核心设计原则：只做字符串算术，不引入 <filesystem>（保持交叉工具链的最小依赖），
 * 不触碰 IO。
 */

#pragma once

#include <cstddef>
#include <string>
#include <string_view>

namespace zhulong {

/**
 * @brief 取路径最后一段（用于 model_id 之类的诊断标识）
 *
 * 语义：忽略尾部分隔符；空串、"///" 这类没有有效段的路径返回空串。
 *
 * ⚠️ 收敛理由：ONNX 与 RKNN 两个推理后端曾各写一份 `file_basename` 且**语义不同**
 *    （一个把尾斜杠当成「取上一级」，另一个把尾斜杠判成空串并让 load_model 失败），
 *    导致同一路径在不同推理后端下行为不一致。model_id 是诊断标识，语义必须唯一。
 *
 * @param path 文件路径（借用）
 * @return std::string 最后一段；无有效段时为空串
 */
inline std::string file_basename(std::string_view path) {
    const std::size_t end = path.find_last_not_of("/\\");
    if (end == std::string_view::npos) {
        return {};
    }
    const std::size_t slash = path.find_last_of("/\\", end);
    const std::size_t begin = (slash == std::string_view::npos) ? 0 : slash + 1;
    return std::string(path.substr(begin, end + 1 - begin));
}

} // namespace zhulong
