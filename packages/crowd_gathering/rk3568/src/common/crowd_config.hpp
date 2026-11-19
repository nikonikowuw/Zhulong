/**
 * @file crowd_config.hpp
 * @brief 人员非法聚集配置解析与数据结构（对应 config.schema.json）
 *
 * 解析是**无第三方依赖**的最小实现：宿主已按 `config.schema.json` 校验过任务配置
 * （`internal/algorithm/config_schema.go` 的 `validateTaskConfig`），这里只做逐键读取与
 * 防御性回落。区间常量必须与 schema 保持逐项一致 —— 两份真值一旦漂移，表现为
 * 「宿主放行、算法静默用默认值」，最是难查。
 */

#pragma once

#include <cmath>
#include <cstddef>
#include <cstdlib>
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

// ---------------------------------------------------------------------------
// 配置区间：逐项对应 config.schema.json 的 minimum / maximum。
// 宿主用 schema 校验，越界值根本到不了这里；下面的区间是**离线调用**（单测、第三方
// 直接链 SDK）时的最后一道防线，越界即回落 fallback，不报错（宿主与包分开交付）。
// ---------------------------------------------------------------------------
inline constexpr float kMinScoreThreshold = 0.10f;
inline constexpr float kMaxScoreThreshold = 1.00f;
inline constexpr int kMinGatherCountThreshold = 2;
inline constexpr int kMaxGatherCountThreshold = 20;
inline constexpr float kMinGatherDistanceRatio = 0.50f;
inline constexpr float kMaxGatherDistanceRatio = 3.00f;
inline constexpr float kMinGatherDurationSeconds = 0.0f;
inline constexpr float kMaxGatherDurationSeconds = 60.0f;

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

// ---------------------------------------------------------------------------
// 检测输入分辨率（进程级，只在 `--config` 生效；任务级下发会被忽略）
// ---------------------------------------------------------------------------

/**
 * @brief 默认检测输入宽度（SCRFD 2.5G 的动态轴）
 *
 * 默认档位按 CCTV 16:9 取 640×384：letterbox 进 640×640 时内容区只有 640×360，
 * 剩下的 280 行全是补边 —— 两者内容分辨率相同，但方形输入的算力与内存白多出约 40%。
 */
inline constexpr int kDefaultDetectInputWidth = 640;
/** @brief 默认检测输入高度（16:9 CCTV 档位；仍须被 `kDetectInputStrideMultiple` 整除） */
inline constexpr int kDefaultDetectInputHeight = 384;
/** @brief FPN 最大 stride：输入边长必须是它的整数倍，否则网格数与模型输出对不上 */
inline constexpr int kDetectInputStrideMultiple = 128;
/** @brief 输入边长下界（至少一个 stride 网格） */
inline constexpr int kMinDetectInputSide = kDetectInputStrideMultiple;
/** @brief 输入边长上界（防误配把内存与算力打爆） */
inline constexpr int kMaxDetectInputSide = 1280;

/**
 * @brief 检测输入分辨率
 */
struct DetectorInputSize {
    int width{kDefaultDetectInputWidth};
    int height{kDefaultDetectInputHeight};
    bool rejected{false}; /**< 调用方给了键但值非法（已回落默认，供调用方留一条记录） */
};

/**
 * @brief 校验检测输入分辨率
 *
 * 不整除即拒绝：布局不符时模型只会返回空输出、没有错误码，这就是一次静默零检出。
 */
inline bool is_valid_detector_input_size(int width, int height) noexcept {
    return width >= kMinDetectInputSide && width <= kMaxDetectInputSide &&
           height >= kMinDetectInputSide && height <= kMaxDetectInputSide &&
           width % kDetectInputStrideMultiple == 0 && height % kDetectInputStrideMultiple == 0;
}

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
inline CrowdConfig resolve_crowd_config(std::string_view json_view, const CrowdConfig& fallback) {
    if (json_view.empty()) {
        return fallback;
    }
    const std::string json(json_view);
    CrowdConfig config = fallback;

    double num_val = 0.0;
    if (find_json_number(json, "score_threshold", num_val) &&
        num_val >= kMinScoreThreshold && num_val <= kMaxScoreThreshold) {
        config.score_threshold = static_cast<float>(num_val);
    }
    if (find_json_number(json, "gather_count_threshold", num_val) &&
        num_val >= static_cast<double>(kMinGatherCountThreshold) &&
        num_val <= static_cast<double>(kMaxGatherCountThreshold)) {
        config.gather_count_threshold = static_cast<int>(num_val);
    }
    if (find_json_number(json, "gather_distance_ratio", num_val) &&
        num_val >= kMinGatherDistanceRatio && num_val <= kMaxGatherDistanceRatio) {
        config.gather_distance_ratio = static_cast<float>(num_val);
    }
    if (find_json_number(json, "gather_duration_seconds", num_val) &&
        num_val >= kMinGatherDurationSeconds && num_val <= kMaxGatherDurationSeconds) {
        config.gather_duration_seconds = static_cast<float>(num_val);
    }

    bool bool_val = false;
    if (find_json_bool(json, "report_person_boxes", bool_val)) {
        config.report_person_boxes = bool_val;
    }

    return config;
}

/**
 * @brief 解析检测输入分辨率；任一维度非法时整体回退（不保留「半套尺寸」）
 */
inline DetectorInputSize resolve_detector_input_size(std::string_view json_view, const DetectorInputSize& fallback) {
    if (json_view.empty()) {
        return fallback;
    }
    const std::string json(json_view);
    DetectorInputSize size = fallback;

    double width = 0.0;
    double height = 0.0;
    if (!find_json_number(json, "detect_input_width", width) ||
        !find_json_number(json, "detect_input_height", height)) {
        return fallback;
    }
    const int width_int = static_cast<int>(width);
    const int height_int = static_cast<int>(height);
    if (!is_valid_detector_input_size(width_int, height_int)) {
        DetectorInputSize rejected = fallback;
        rejected.rejected = true;
        return rejected;
    }
    size.width = width_int;
    size.height = height_int;
    return size;
}

} // namespace zhulong::crowd
