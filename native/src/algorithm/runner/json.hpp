/**
 * @file json.hpp
 * @brief 算法 Runner 诊断输出专用轻量级 JSON 字符串安全转义器
 *
 * 核心设计原则：
 * 1. 严格转义规约：转义双引号、反斜杠、常见 ASCII 转义控制字符（\\b, \\f, \\n, \\r, \\t）；
 * 2. 控制字符十六进制化：对 U+0020 以下所有控制字符转义为标准 \\u00xx 六字符格式，杜绝任何原始换行或控制字符破坏 Runner 单行 JSON 输出流。
 */

#pragma once

#include <string>

namespace zhulong::runner {

/**
 * @brief 对任意原始字符串添加首尾双引号并转义其内部特殊字符与不可打印控制字符
 *
 * @param value 原始输入字符串
 * @return std::string 包含首尾双引号的标准 JSON 编码安全字符串
 */
inline std::string json_quote(const std::string& value) {
    static constexpr char hex_digits[] = "0123456789abcdef";
    std::string escaped;
    escaped.reserve(value.size() + 2);
    escaped.push_back('"');

    for (unsigned char character : value) {
        switch (character) {
            case '"': escaped += "\\\""; break;
            case '\\': escaped += "\\\\"; break;
            case '\b': escaped += "\\b"; break;
            case '\f': escaped += "\\f"; break;
            case '\n': escaped += "\\n"; break;
            case '\r': escaped += "\\r"; break;
            case '\t': escaped += "\\t"; break;
            default:
                if (character < 0x20) {
                    escaped += "\\u00";
                    escaped.push_back(hex_digits[character >> 4]);
                    escaped.push_back(hex_digits[character & 0x0f]);
                } else {
                    escaped.push_back(static_cast<char>(character));
                }
        }
    }

    escaped.push_back('"');
    return escaped;
}

} // namespace zhulong::runner

