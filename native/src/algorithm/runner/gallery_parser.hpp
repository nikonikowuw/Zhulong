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

inline FaceTemplate parse_upsert_payload(const uint8_t* data, size_t size) {
    if (!data || size == 0) return {};
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

} // namespace zhulong::runner
