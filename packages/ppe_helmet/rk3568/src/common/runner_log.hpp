/**
 * @file runner_log.hpp
 * @brief 算法包统一日志行（单行 JSON，宿主 Runner 直接采集）
 *
 * 格式固定为 `{"zhulongLog":true,"level":...,"module":...,"streamId":0,...}`；
 * 打断行（多行 JSON）会让采集端只取到第一行，所以 message/detail 里的换行必须转义。
 */

#pragma once

#include <cstdio>
#include <string>
#include <string_view>

namespace zhulong::ppe {
namespace detail {

/** @brief 把字符串转义成 JSON 字符串字面量内容（不含首尾引号） */
inline std::string json_escape(std::string_view input) {
    std::string out;
    out.reserve(input.size() + 8);
    for (const unsigned char ch : input) {
        switch (ch) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default:
                if (ch < 0x20) {
                    char buffer[8] = {};
                    std::snprintf(buffer, sizeof(buffer), "\\u%04x", static_cast<unsigned>(ch));
                    out += buffer;
                } else {
                    out.push_back(static_cast<char>(ch));
                }
                break;
        }
    }
    return out;
}

/** @brief module 只允许 [A-Za-z0-9_.-]，非法字符替换为 '_'，保证日志行可被解析 */
inline std::string sanitize_module(std::string_view module) {
    std::string out(module);
    for (char& ch : out) {
        const bool legal = (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
                           (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-';
        if (!legal) {
            ch = '_';
        }
    }
    return out;
}

inline void write_log_line(const char* level, std::string_view module, std::string_view message,
                           std::string_view detail) {
    const std::string level_text = json_escape(level);
    const std::string module_text = sanitize_module(module);
    const std::string message_text = json_escape(message);
    const std::string detail_text = json_escape(detail);
    std::fprintf(stderr,
                 "{\"zhulongLog\":true,\"level\":\"%s\",\"module\":\"%s\",\"streamId\":0,"
                 "\"message\":\"%s\",\"detail\":\"%s\"}\n",
                 level_text.c_str(), module_text.c_str(), message_text.c_str(), detail_text.c_str());
}

} // namespace detail

inline void log_info(std::string_view module, std::string_view message, std::string_view detail = {}) {
    detail::write_log_line("info", module, message, detail);
}

inline void log_warning(std::string_view module, std::string_view message, std::string_view detail = {}) {
    detail::write_log_line("warn", module, message, detail);
}

} // namespace zhulong::ppe
