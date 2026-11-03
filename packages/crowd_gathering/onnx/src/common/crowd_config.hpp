/**
 * @file crowd_config.hpp
 * @brief 人员非法聚集配置解析与数据结构（对应 config.schema.json）
 */

#pragma once

#include <cmath>
#include <cstddef>
#include <string>
#include <string_view>

namespace zhulong::crowd {

/** @brief 默认人体检测置信度阈值 */
inline constexpr float kDefaultScoreThreshold = 0.50f;
/** @brief 默认聚集最小人数阈值 */
inline constexpr int kDefaultGatherCountThreshold = 3;
/** @brief 默认聚集相对距离倍数（平均身高的倍数） */
inline constexpr float kDefaultGatherDistanceRatio = 1.20f;
/** @brief 默认聚集持续告警时长（秒） */
inline constexpr float kDefaultGatherDurationSeconds = 5.0f;
/** @brief 默认是否上报个体人框 */
inline constexpr bool kDefaultReportPersonBoxes = false;

/**
 * @brief 人员聚集运行期配置参数
 */
struct CrowdConfig {
    float score_threshold{kDefaultScoreThreshold};
    int gather_count_threshold{kDefaultGatherCountThreshold};
    float gather_distance_ratio{kDefaultGatherDistanceRatio};
    float gather_duration_seconds{kDefaultGatherDurationSeconds};
    bool report_person_boxes{kDefaultReportPersonBoxes};
};

inline bool is_json_delimiter(char ch) noexcept {
    return ch == ',' || ch == '}' || ch == ']' || static_cast<unsigned char>(ch) <= ' ';
}

inline bool find_json_number(const std::string& json, std::string_view key, double& out_value) {
    const std::string quoted_key = "\"" + std::string(key) + "\"";
    for (size_t pos = json.find(quoted_key); pos != std::string::npos; pos = json.find(quoted_key, pos + 1)) {
        size_t before = pos;
        while (before > 0 && static_cast<unsigned char>(json[before - 1]) <= ' ') {
            --before;
        }
        if (before == 0) {
            continue;
        }
        const char preceding = json[before - 1];
        if (preceding != '{' && preceding != ',') {
            continue;
        }

        const size_t colon = json.find(':', pos + quoted_key.size());
        if (colon == std::string::npos) {
            continue;
        }
        const size_t value_start = json.find_first_not_of(" \t\r\n", colon + 1);
        if (value_start == std::string::npos) {
            continue;
        }
        try {
            size_t parsed = 0;
            const double value = std::stod(json.substr(value_start), &parsed);
            if (parsed == 0 || !std::isfinite(value)) {
                continue;
            }
            out_value = value;
            return true;
        } catch (const std::exception&) {
            continue;
        }
    }
    return false;
}

inline bool find_json_bool(const std::string& json, std::string_view key, bool& out_value) {
    const std::string quoted_key = "\"" + std::string(key) + "\"";
    for (size_t pos = json.find(quoted_key); pos != std::string::npos; pos = json.find(quoted_key, pos + 1)) {
        size_t before = pos;
        while (before > 0 && static_cast<unsigned char>(json[before - 1]) <= ' ') {
            --before;
        }
        if (before == 0) {
            continue;
        }
        const char preceding = json[before - 1];
        if (preceding != '{' && preceding != ',') {
            continue;
        }

        const size_t colon = json.find(':', pos + quoted_key.size());
        if (colon == std::string::npos) {
            continue;
        }
        const size_t value_start = json.find_first_not_of(" \t\r\n", colon + 1);
        if (value_start == std::string::npos) {
            continue;
        }
        if (json.compare(value_start, 4, "true") == 0) {
            const size_t end_pos = value_start + 4;
            if (end_pos == json.size() || is_json_delimiter(json[end_pos])) {
                out_value = true;
                return true;
            }
        }
        if (json.compare(value_start, 5, "false") == 0) {
            const size_t end_pos = value_start + 5;
            if (end_pos == json.size() || is_json_delimiter(json[end_pos])) {
                out_value = false;
                return true;
            }
        }
    }
    return false;
}

inline CrowdConfig resolve_crowd_config(std::string_view json_view, const CrowdConfig& fallback) {
    if (json_view.empty()) {
        return fallback;
    }
    const std::string json(json_view);
    CrowdConfig config = fallback;

    double num_val = 0.0;
    if (find_json_number(json, "score_threshold", num_val) && num_val >= 0.05 && num_val <= 1.0) {
        config.score_threshold = static_cast<float>(num_val);
    }
    if (find_json_number(json, "gather_count_threshold", num_val) && num_val >= 2.0 && num_val <= 100.0) {
        config.gather_count_threshold = static_cast<int>(num_val);
    }
    if (find_json_number(json, "gather_distance_ratio", num_val) && num_val >= 0.1 && num_val <= 10.0) {
        config.gather_distance_ratio = static_cast<float>(num_val);
    }
    if (find_json_number(json, "gather_duration_seconds", num_val) && num_val >= 0.0 && num_val <= 3600.0) {
        config.gather_duration_seconds = static_cast<float>(num_val);
    }

    bool bool_val = false;
    if (find_json_bool(json, "report_person_boxes", bool_val)) {
        config.report_person_boxes = bool_val;
    }

    return config;
}

} // namespace zhulong::crowd
