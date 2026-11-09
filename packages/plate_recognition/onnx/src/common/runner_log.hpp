/**
 * @file runner_log.hpp
 * @brief 包侧诊断通道（宿主可解析的原生日志行）
 *
 * 为什么不能直接 `fprintf(stderr, "text\n")`：宿主的 runner 日志分类器
 * （`internal/algorunner/runner_log.go`）只认三类行 —— 骨架协议事件、
 * `{"zhulongLog":true,...}` 原生日志、以及**被丢弃**的其它文本（只留 `line_bytes`）。
 * 也就是说裸文本诊断在算法作者自己的终端里看得见，在运维看到的宿主日志里等于没写。
 *
 * 形状与 `packages/crowd_gathering/onnx/src/common/runner_log.hpp` 逐字段一致（同一套宿主
 * 解析器），只把命名空间换成车牌包。
 *
 * 约束：
 *   - 单行 JSON，字段顺序固定，无换行；
 *   - `module` 只允许 `[A-Za-z0-9_.-]`，否则宿主判为 rejected；
 *   - `level` 必须是 debug / info / warn / error。
 * 刻意不做格式化库：告警路径上只允许一次定长写。
 */

#pragma once

#include <cstddef>
#include <cstdio>
#include <string_view>

namespace zhulong::plate {

namespace detail {

/** @brief 把 `value` 写进 `out`（已转义的最小 JSON 字符串体），满则截断 */
inline size_t append_escaped(char* out, size_t capacity, std::string_view value) noexcept {
    size_t used = 0;
    for (const char raw : value) {
        const unsigned char ch = static_cast<unsigned char>(raw);
        if (used + 2 >= capacity) {
            break;
        }
        if (ch < 0x20 || ch >= 0x80) {
            // 控制字符与非 ASCII 一律折成 '?'：日志行必须始终是合法 JSON，
            // 半截 UTF-8 会让宿主的解析器整行作废（车牌号码正是 CJK，必踩）。
            out[used++] = '?';
            continue;
        }
        if (ch == '"' || ch == '\\') {
            out[used++] = '\\';
        }
        out[used++] = static_cast<char>(ch);
    }
    out[used] = '\0';
    return used;
}

} // namespace detail

/**
 * @brief 输出一条宿主可解析的原生日志记录
 *
 * @param level debug / info / warn / error
 * @param module 模块名（本包固定用 `plate_recognition`）
 * @param message 诊断正文（宿主只统计字节数，不落原文）
 * @param detail 细分原因（同样不落宿主原文；手工跑包时可见）
 */
inline void log_record(std::string_view level, std::string_view module, std::string_view message,
                       std::string_view detail = {}) noexcept {
    char escaped[384];
    detail::append_escaped(escaped, sizeof(escaped), message);
    char escaped_detail[192];
    detail::append_escaped(escaped_detail, sizeof(escaped_detail), detail);

    char line[640];
    const int written = std::snprintf(
        line, sizeof(line),
        "{\"zhulongLog\":true,\"level\":\"%.*s\",\"module\":\"%.*s\",\"streamId\":0,"
        "\"message\":\"%s\",\"detail\":\"%s\"}\n",
        static_cast<int>(level.size()), level.data(),
        static_cast<int>(module.size()), module.data(),
        escaped, escaped_detail);
    if (written <= 0 || static_cast<size_t>(written) >= sizeof(line)) {
        return;
    }
    // 单次写入：被信号打断时不会留下半行 JSON 把宿主解析器带偏。
    // 失败**刻意**不处理：本函数在告警路径上是 noexcept，写不出去也没有第二个通道可退，
    // 显式 `(void)` 表达「已评估、不处理」，而不是漏了返回值。
    (void)std::fwrite(line, 1, static_cast<size_t>(written), stderr);
    (void)std::fflush(stderr);
}

/** @brief 调试级记录（默认不产生任何输出成本，只在真的失败路径调用） */
inline void log_debug(std::string_view module, std::string_view message,
                      std::string_view detail = {}) noexcept {
    log_record("debug", module, message, detail);
}

/** @brief 告警级记录 */
inline void log_warning(std::string_view module, std::string_view message,
                        std::string_view detail = {}) noexcept {
    log_record("warn", module, message, detail);
}

} // namespace zhulong::plate
