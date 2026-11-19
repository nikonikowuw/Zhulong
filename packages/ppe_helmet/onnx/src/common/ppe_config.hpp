/**
 * @file ppe_config.hpp
 * @brief 安全帽穿戴检测配置解析与数据结构（对应 config.schema.json）
 *
 * 解析是**无第三方依赖**的最小实现：宿主已按 `config.schema.json` 校验过任务配置，
 * 这里只做逐键读取与防御性回落。区间常量必须与 schema 保持逐项一致 —— 两份真值一旦
 * 漂移，表现为「宿主放行、算法静默用默认值」，最是难查。
 */

#pragma once

#include <cmath>
#include <cstddef>
#include <cstdlib>
#include <string>
#include <string_view>

namespace zhulong::ppe {

/** @brief 默认检测置信度阈值（person / head_helmet / head_nohelmet 共用） */
inline constexpr float kDefaultScoreThreshold = 0.25f;
/** @brief 默认 NMS IoU 阈值 */
inline constexpr float kDefaultNmsThreshold = 0.45f;
/** @brief 默认参与安全帽研判的人体最小高度（归一化，相对画面高度） */
inline constexpr float kDefaultMinPersonHeightRatio = 0.08f;
/** @brief 默认是否上报合规人员（调试/画框用，不产生告警） */
inline constexpr bool kDefaultReportCompliant = false;
/** @brief 默认连续确认帧数：单帧 head_nohelmet 可能来自侧脸/逆光，两帧更稳 */
inline constexpr int kDefaultConfirmFrames = 2;

// ---------------------------------------------------------------------------
// 配置区间：逐项对应 config.schema.json 的 minimum / maximum。
// 宿主用 schema 校验，越界值根本到不了这里；下面的区间是**离线调用**（单测、第三方
// 直接链 SDK）时的最后一道防线，越界即回落 fallback，不报错（宿主与包分开交付）。
// ---------------------------------------------------------------------------
inline constexpr float kMinScoreThreshold = 0.05f;
inline constexpr float kMaxScoreThreshold = 0.95f;
inline constexpr float kMinNmsThreshold = 0.10f;
inline constexpr float kMaxNmsThreshold = 0.90f;
inline constexpr float kMinMinPersonHeightRatio = 0.02f;
inline constexpr float kMaxMinPersonHeightRatio = 0.50f;
inline constexpr int kMinConfirmFrames = 1;
inline constexpr int kMaxConfirmFrames = 10;

/**
 * @brief 安全帽穿戴检测运行期配置参数
 */
struct PpeConfig {
    float score_threshold{kDefaultScoreThreshold};
    float nms_threshold{kDefaultNmsThreshold};
    float min_person_height_ratio{kDefaultMinPersonHeightRatio};
    bool report_compliant{kDefaultReportCompliant};
    int confirm_frames{kDefaultConfirmFrames};
};

// ---------------------------------------------------------------------------
// 最小 JSON 取值助手
// ---------------------------------------------------------------------------

/** @brief JSON 值的结束判定（`,` `}` `]` 或 ASCII 空白） */
inline bool is_json_delimiter(char ch) noexcept {
    return ch == ',' || ch == '}' || ch == ']' || static_cast<unsigned char>(ch) <= ' ';
}

/**
 * @brief 定位顶层对象里 `"key"` 对应值的首字节下标
 *
 * 逐字符扫描并跟踪对象/数组深度与字符串状态，只认**顶层对象成员**位置上的键：
 *   - `{"a":{"key":1}}` 里嵌套的 `key` 不会被误取；
 *   - `{"note":"key:1"}` 里字符串内容不会被误取；
 *   - 键名之后的第一个非空白字符必须是 `:`。
 *
 * @param json JSON 原文
 * @param key 键名（不带引号）
 * @param out_value_start 值首字节下标（已跳过空白）
 * @return 找到返回 true
 */
inline bool find_json_value_start(const std::string& json, std::string_view key, size_t& out_value_start) {
    size_t depth = 0;
    bool in_string = false;
    bool escaped = false;
    size_t string_start = 0;

    for (size_t i = 0; i < json.size(); ++i) {
        const char ch = json[i];
        if (in_string) {
            if (escaped) {
                escaped = false;
            } else if (ch == '\\') {
                escaped = true;
            } else if (ch == '"') {
                in_string = false;
                // 刚闭合的字符串：位于顶层对象内、内容等于键名、后面紧跟 ':' 才算命中
                const size_t content_len = i - string_start - 1;
                if (depth != 1 || content_len != key.size() ||
                    json.compare(string_start + 1, content_len, key) != 0) {
                    continue;
                }
                const size_t colon = json.find_first_not_of(" \t\r\n", i + 1);
                if (colon == std::string::npos || json[colon] != ':') {
                    continue;
                }
                const size_t value_start = json.find_first_not_of(" \t\r\n", colon + 1);
                if (value_start == std::string::npos) {
                    continue;
                }
                out_value_start = value_start;
                return true;
            }
            continue;
        }
        if (ch == '"') {
            in_string = true;
            string_start = i;
        } else if (ch == '{' || ch == '[') {
            ++depth;
        } else if (ch == '}' || ch == ']') {
            if (depth > 0) {
                --depth;
            }
        }
    }
    return false;
}

/**
 * @brief 读取数值型键（拒绝非有限值与「数字后面还粘着垃圾」的输入）
 *
 * 用 `strtod` 而不是 `std::stod`：后者要先 `substr` 出一份临时串，热路径上每帧每键一次分配。
 */
inline bool find_json_number(const std::string& json, std::string_view key, double& out_value) {
    size_t value_start = 0;
    if (!find_json_value_start(json, key, value_start)) {
        return false;
    }
    const char* begin = json.c_str() + value_start;
    char* end = nullptr;
    const double value = std::strtod(begin, &end);
    if (end == begin || !std::isfinite(value)) {
        return false;
    }
    // `3x` / `1.0.0` 这类输入不能当 3 与 1.0 用掉
    if (*end != '\0' && !is_json_delimiter(*end)) {
        return false;
    }
    out_value = value;
    return true;
}

/**
 * @brief 读取布尔型键
 */
inline bool find_json_bool(const std::string& json, std::string_view key, bool& out_value) {
    size_t value_start = 0;
    if (!find_json_value_start(json, key, value_start)) {
        return false;
    }
    for (const std::string_view literal : {std::string_view{"true"}, std::string_view{"false"}}) {
        if (json.compare(value_start, literal.size(), literal) != 0) {
            continue;
        }
        const size_t end_pos = value_start + literal.size();
        if (end_pos == json.size() || is_json_delimiter(json[end_pos])) {
            out_value = (literal == "true");
            return true;
        }
    }
    return false;
}

/**
 * @brief 解析任务级/进程级配置；未下发或值越界时沿用 fallback
 *
 * @param json_view 配置原文（未下发时为空视图）
 * @param fallback 回退配置（进程级配置作为任务级配置的回退）
 */
inline PpeConfig resolve_ppe_config(std::string_view json_view, const PpeConfig& fallback) {
    if (json_view.empty()) {
        return fallback;
    }
    const std::string json(json_view);
    PpeConfig config = fallback;

    double num_val = 0.0;
    if (find_json_number(json, "score_threshold", num_val) &&
        num_val >= kMinScoreThreshold && num_val <= kMaxScoreThreshold) {
        config.score_threshold = static_cast<float>(num_val);
    }
    if (find_json_number(json, "nms_threshold", num_val) &&
        num_val >= kMinNmsThreshold && num_val <= kMaxNmsThreshold) {
        config.nms_threshold = static_cast<float>(num_val);
    }
    if (find_json_number(json, "min_person_height_ratio", num_val) &&
        num_val >= kMinMinPersonHeightRatio && num_val <= kMaxMinPersonHeightRatio) {
        config.min_person_height_ratio = static_cast<float>(num_val);
    }
    if (find_json_number(json, "confirm_frames", num_val) &&
        num_val >= static_cast<double>(kMinConfirmFrames) &&
        num_val <= static_cast<double>(kMaxConfirmFrames) &&
        std::floor(num_val) == num_val) {
        config.confirm_frames = static_cast<int>(num_val);
    }

    bool bool_val = false;
    if (find_json_bool(json, "report_compliant", bool_val)) {
        config.report_compliant = bool_val;
    }

    return config;
}

} // namespace zhulong::ppe
