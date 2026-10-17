/**
 * @file gallery_parser.hpp
 * @brief 底库下行 IPC 报文（SYNC/UPSERT/DELETE）轻量级安全解析器
 */

#pragma once

#include "gallery_index.hpp"
#include <string_view>
#include <charconv>
#include <cstdlib>
#include <cstdint>

namespace zhulong::runner {

inline void skip_whitespace(std::string_view sv, size_t& i) {
    while (i < sv.size() && (sv[i] == ' ' || sv[i] == '\t' || sv[i] == '\r' || sv[i] == '\n')) {
        ++i;
    }
}

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
                    // 跳过 4 个 hex 字符
                    if (i + 4 <= sv.size()) i += 4;
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
        if (i < sv.size() && sv[i] == ']') {
            ++i;
            break;
        }
        char* end = nullptr;
        const char* start_ptr = sv.data() + i;
        float val = std::strtof(start_ptr, &end);
        if (end > start_ptr) {
            res.push_back(val);
            i += static_cast<size_t>(end - start_ptr);
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
