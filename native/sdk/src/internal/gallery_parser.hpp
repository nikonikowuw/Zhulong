/**
 * @file gallery_parser.hpp
 * @brief 底库下行 IPC 报文（SYNC/UPSERT/DELETE）轻量级安全解析器
 */

#pragma once

#include "internal/gallery_index.hpp"
#include <string>
#include <string_view>
#include <charconv>
#include <cstdlib>
#include <cstdint>
#include <cstring>

namespace zhulong::runner {

inline void skip_whitespace(std::string_view sv, size_t& i) {
    while (i < sv.size() && (sv[i] == ' ' || sv[i] == '\t' || sv[i] == '\r' || sv[i] == '\n')) {
        ++i;
    }
}

namespace detail {

inline bool parse_hex4(std::string_view sv, size_t pos, uint32_t& out) {
    if (pos + 4 > sv.size()) return false;
    uint32_t value = 0;
    for (size_t k = 0; k < 4; ++k) {
        const char ch = sv[pos + k];
        uint32_t digit = 0;
        if (ch >= '0' && ch <= '9') {
            digit = static_cast<uint32_t>(ch - '0');
        } else if (ch >= 'a' && ch <= 'f') {
            digit = static_cast<uint32_t>(10 + (ch - 'a'));
        } else if (ch >= 'A' && ch <= 'F') {
            digit = static_cast<uint32_t>(10 + (ch - 'A'));
        } else {
            return false;
        }
        value = (value << 4) | digit;
    }
    out = value;
    return true;
}

inline void append_utf8(std::string& out, uint32_t cp) {
    if (cp <= 0x7FU) {
        out.push_back(static_cast<char>(cp));
    } else if (cp <= 0x7FFU) {
        out.push_back(static_cast<char>(0xC0U | (cp >> 6)));
        out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else if (cp <= 0xFFFFU) {
        out.push_back(static_cast<char>(0xE0U | (cp >> 12)));
        out.push_back(static_cast<char>(0x80U | ((cp >> 6) & 0x3FU)));
        out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else if (cp <= 0x10FFFFU) {
        out.push_back(static_cast<char>(0xF0U | (cp >> 18)));
        out.push_back(static_cast<char>(0x80U | ((cp >> 12) & 0x3FU)));
        out.push_back(static_cast<char>(0x80U | ((cp >> 6) & 0x3FU)));
        out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    }
}

inline bool is_json_number_char(char c) {
    return (c >= '0' && c <= '9') || c == '.' || c == '+' || c == '-' || c == 'e' || c == 'E';
}

} // namespace detail

inline std::string parse_json_string(std::string_view sv, size_t& i) {
    skip_whitespace(sv, i);
    if (i >= sv.size() || sv[i] != '"') return "";
    ++i;
    std::string res;
    while (i < sv.size()) {
        char c = sv[i++];
        if (c == '"') break;
        if (c == '\\' && i < sv.size()) {
            char esc = sv[i++];
            switch (esc) {
                case '"': res += '"'; break;
                case '\\': res += '\\'; break;
                case '/': res += '/'; break;
                case 'b': res += '\b'; break;
                case 'f': res += '\f'; break;
                case 'n': res += '\n'; break;
                case 'r': res += '\r'; break;
                case 't': res += '\t'; break;
                case 'u': {
                    uint32_t unit = 0;
                    if (!detail::parse_hex4(sv, i, unit)) {
                        break;
                    }
                    i += 4;
                    if (unit >= 0xD800U && unit <= 0xDBFFU) {
                        uint32_t low = 0;
                        if (i + 6 <= sv.size() && sv[i] == '\\' && sv[i + 1] == 'u' &&
                            detail::parse_hex4(sv, i + 2, low) &&
                            low >= 0xDC00U && low <= 0xDFFFU) {
                            i += 6;
                            const uint32_t cp = 0x10000U + (((unit - 0xD800U) << 10) | (low - 0xDC00U));
                            detail::append_utf8(res, cp);
                        } else {
                            // 孤立高代理项：输出 U+FFFD，避免写出非法 UTF-8 代理字节序列
                            detail::append_utf8(res, 0xFFFDU);
                        }
                    } else if (unit >= 0xDC00U && unit <= 0xDFFFU) {
                        // 孤立低代理项：输出 U+FFFD
                        detail::append_utf8(res, 0xFFFDU);
                    } else {
                        detail::append_utf8(res, unit);
                    }
                    break;
                }
                default: res += esc; break;
            }
        } else {
            res += c;
        }
    }
    return res;
}

inline int parse_json_int(std::string_view sv, size_t& i) {
    skip_whitespace(sv, i);
    size_t start = i;
    if (i < sv.size() && (sv[i] == '-' || sv[i] == '+')) ++i;
    while (i < sv.size() && sv[i] >= '0' && sv[i] <= '9') ++i;
    if (start == i) return 0;
    int val = 0;
    std::from_chars(sv.data() + start, sv.data() + i, val);
    return val;
}

inline std::vector<float> parse_float_array(std::string_view sv, size_t& i) {
    skip_whitespace(sv, i);
    std::vector<float> res;
    if (i >= sv.size() || sv[i] != '[') return res;
    ++i; // skip '['
    res.reserve(512);

    while (i < sv.size()) {
        skip_whitespace(sv, i);
        if (i >= sv.size()) break;
        if (sv[i] == ']') {
            ++i;
            break;
        }

        // 先在 string_view 边界内扫描数字 token，再拷贝到 NUL 结尾缓冲供 strtof 解析：
        // IPC 报文底层是无 NUL 结尾的 std::vector<uint8_t>，直接传 sv.data() + i
        // 给 strtof 会在截断/畸形报文上触发堆越界读；同时避免依赖 GCC 10 sysroot
        // 缺失的浮点 std::from_chars。
        const size_t token_start = i;
        size_t token_end = token_start;
        while (token_end < sv.size() && detail::is_json_number_char(sv[token_end])) {
            ++token_end;
        }
        const size_t token_len = token_end - token_start;
        if (token_len > 0) {
            char stack_buf[64];
            std::string heap_buf;
            const char* token_ptr = nullptr;
            if (token_len < sizeof(stack_buf)) {
                std::memcpy(stack_buf, sv.data() + token_start, token_len);
                stack_buf[token_len] = '\0';
                token_ptr = stack_buf;
            } else {
                heap_buf.assign(sv.data() + token_start, token_len);
                token_ptr = heap_buf.c_str();
            }

            char* end = nullptr;
            float val = std::strtof(token_ptr, &end);
            if (end > token_ptr) {
                res.push_back(val);
                i = token_start + static_cast<size_t>(end - token_ptr);
            } else {
                ++i; // 防止死循环
            }
        } else {
            ++i; // 防止死循环
        }
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ',') {
            ++i;
        }
    }
    return res;
}

inline FaceTemplate parse_face_item(std::string_view sv, size_t& i) {
    skip_whitespace(sv, i);
    FaceTemplate tmpl;
    if (i >= sv.size() || sv[i] != '{') return tmpl;
    ++i;

    while (i < sv.size()) {
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == '}') {
            ++i;
            break;
        }
        std::string key = parse_json_string(sv, i);
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ':') ++i;

        if (key == "face_id") {
            tmpl.face_id = parse_json_string(sv, i);
        } else if (key == "person_id") {
            tmpl.person_id = parse_json_string(sv, i);
        } else if (key == "person_name") {
            tmpl.person_name = parse_json_string(sv, i);
        } else if (key == "image_path") {
            tmpl.image_path = parse_json_string(sv, i);
        } else if (key == "features") {
            tmpl.features = parse_float_array(sv, i);
        } else {
            // 跳过未知字段
            skip_whitespace(sv, i);
            if (i < sv.size() && sv[i] == '"') {
                parse_json_string(sv, i);
            } else if (i < sv.size() && sv[i] == '[') {
                int depth = 1;
                ++i;
                while (i < sv.size() && depth > 0) {
                    if (sv[i] == '[') ++depth;
                    else if (sv[i] == ']') --depth;
                    ++i;
                }
            } else if (i < sv.size() && sv[i] == '{') {
                int depth = 1;
                ++i;
                while (i < sv.size() && depth > 0) {
                    if (sv[i] == '{') ++depth;
                    else if (sv[i] == '}') --depth;
                    ++i;
                }
            } else {
                while (i < sv.size() && sv[i] != ',' && sv[i] != '}') ++i;
            }
        }

        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ',') ++i;
    }
    return tmpl;
}

struct ParsedSyncShard {
    std::string sync_id;
    int shard_index{0};
    int shard_total{0};
    std::vector<FaceTemplate> items;
};

inline ParsedSyncShard parse_sync_shard_payload(const uint8_t* data, size_t size) {
    ParsedSyncShard shard;
    if (!data || size == 0) return shard;
    std::string_view sv(reinterpret_cast<const char*>(data), size);
    size_t i = 0;
    skip_whitespace(sv, i);
    if (i >= sv.size() || sv[i] != '{') return shard;
    ++i;

    while (i < sv.size()) {
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == '}') {
            ++i;
            break;
        }
        std::string key = parse_json_string(sv, i);
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ':') ++i;

        if (key == "sync_id") {
            shard.sync_id = parse_json_string(sv, i);
        } else if (key == "shard_index") {
            shard.shard_index = parse_json_int(sv, i);
        } else if (key == "shard_total") {
            shard.shard_total = parse_json_int(sv, i);
        } else if (key == "items") {
            skip_whitespace(sv, i);
            if (i < sv.size() && sv[i] == '[') {
                ++i;
                while (i < sv.size()) {
                    skip_whitespace(sv, i);
                    if (i < sv.size() && sv[i] == ']') {
                        ++i;
                        break;
                    }
                    shard.items.push_back(parse_face_item(sv, i));
                    skip_whitespace(sv, i);
                    if (i < sv.size() && sv[i] == ',') ++i;
                }
            }
        } else {
            // 跳过未知字段
            while (i < sv.size() && sv[i] != ',' && sv[i] != '}') ++i;
        }

        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ',') ++i;
    }
    return shard;
}

inline FaceTemplate parse_upsert_payload(const uint8_t* data, size_t size) {    if (!data || size == 0) return {};
    std::string_view sv(reinterpret_cast<const char*>(data), size);
    size_t i = 0;
    return parse_face_item(sv, i);
}

struct ParsedDelete {
    std::string face_id;
    std::string person_id;
};

inline ParsedDelete parse_delete_payload(const uint8_t* data, size_t size) {
    ParsedDelete del;
    if (!data || size == 0) return del;
    std::string_view sv(reinterpret_cast<const char*>(data), size);
    size_t i = 0;
    skip_whitespace(sv, i);
    if (i >= sv.size() || sv[i] != '{') return del;
    ++i;

    while (i < sv.size()) {
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == '}') {
            ++i;
            break;
        }
        std::string key = parse_json_string(sv, i);
        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ':') ++i;

        if (key == "face_id") {
            del.face_id = parse_json_string(sv, i);
        } else if (key == "person_id") {
            del.person_id = parse_json_string(sv, i);
        } else {
            while (i < sv.size() && sv[i] != ',' && sv[i] != '}') ++i;
        }

        skip_whitespace(sv, i);
        if (i < sv.size() && sv[i] == ',') ++i;
    }
    return del;
}

/**
 * @brief 将单个已解析的底库同步分片幂等地聚合进上下文。
 *
 * 提取自 runner.cpp 的 MSG_GALLERY_SYNC 处理分支，使该逻辑可被单元测试直接覆盖。
 *
 * ⚠️ 两条关键约束（缺一即会永久污染底库）：
 *  1. **索引范围门禁**：shard_index 必须落在 [0, shard_total) 内。越界或负值分片
 *     直接丢弃且不改变 ctx 任何状态 —— 否则 received_shards.size() 会被污染，
 *     而 is_complete() 只看 size，可能提前判定完成。
 *  2. **分片级幂等**：同一 shard_index 重复到达时，必须跳过 items 累加。
 *     insert() 是幂等的但 push_back 不是，只靠 insert 会让 collected_items
 *     重复膨胀，最终经 gallery_index.reset() 永久污染底库。
 *
 * @param ctx 分片聚合上下文（可能被本函数修改）
 * @param shard 已解析的分片
 * @return true 表示本分片被接受（首次到达）；false 表示被丢弃（越界）或重复
 *
 * ⚠️ 调用方必须**只在返回 true 且 is_complete() 为真时**才应用底库。
 *    原因：收齐后 `received_shards` 保持满，`is_complete()` 会**恒为真**，
 *    若对重复分片也触发 reset，会因 `collected_items` 已被 move 而**清空底库**。
 */
inline bool accumulate_sync_shard(ShardedSyncContext& ctx, const ParsedSyncShard& shard) {
    if (shard.shard_total <= 0) {
        return false;
    }
    if (shard.shard_index < 0 || shard.shard_index >= shard.shard_total) {
        return false; // 越界分片：直接丢弃，不改变 ctx 任何状态
    }
    if (ctx.sync_id != shard.sync_id || ctx.is_expired()) {
        ctx.start_sync(shard.sync_id, shard.shard_total);
    }
    if (!ctx.received_shards.insert(shard.shard_index).second) {
        return false; // 重复分片：跳过 items 累加
    }
    for (const auto& item : shard.items) {
        ctx.collected_items.push_back(item);
    }
    return true;
}

} // namespace zhulong::runner
