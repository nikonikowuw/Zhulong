/**
 * @file match_config.hpp
 * @brief 算法运行参数（识别阈值 / Top-K / 检测置信度 / 最小人脸 / 纯数据质量门限 / 未识别过滤）的默认值与扁平 JSON 解析
 *
 * 为什么单独抽出来：同一套参数有**两个来源**——进程级（`--config` / `init`）与
 * 任务级（宿主逐路流下发的 `MSG_TASK_CONFIG`），两者的取值范围、缺省回落语义必须完全
 * 一致。写两遍的结局是「改了一个忘了另一个」，而症状只是「某一路流的阈值不对」。
 *
 * 另外它把这段逻辑变成**不需要模型**就能单测的纯函数（`make unit-test` 覆盖）：
 * 阈值解析错了不会崩、只会让识别结果对不上配置，靠端到端测试几乎抓不住。
 */

#pragma once

#include <cerrno>
#include <cmath>
#include <cstddef>
#include <cstdlib>
#include <cstring>
#include <exception>
#include <limits>
#include <string>
#include <string_view>

namespace zhulong::face {

/** @brief 比对命中阈值默认值（config.schema.json 的 threshold 默认值，两处必须同步） */
inline constexpr float kDefaultMatchThreshold = 0.40f;
/** @brief Top-K 默认值（config.schema.json 的 top_k 默认值，两处必须同步） */
inline constexpr size_t kDefaultMatchTopK = 3;
/** @brief Top-K 上限（config.schema.json 的 top_k maximum，两处必须同步） */
inline constexpr size_t kMaxMatchTopK = 20;
/** @brief Top-K 下限（schema minimum；0 会让候选人列表恒为空） */
inline constexpr size_t kMinMatchTopK = 1;
/** @brief 阈值下限（余弦相似度截断到 [0,1] 后的合法域） */
inline constexpr float kMinMatchThreshold = 0.0f;
/** @brief 阈值上限 */
inline constexpr float kMaxMatchThreshold = 1.0f;

/** @brief 人脸检测置信度默认值 */
inline constexpr float kDefaultFaceDetThreshold = 0.50f;
inline constexpr float kMinFaceDetThreshold = 0.10f;
inline constexpr float kMaxFaceDetThreshold = 1.00f;

/** @brief 人体检测置信度默认值 */
inline constexpr float kDefaultPersonDetThreshold = 0.50f;
inline constexpr float kMinPersonDetThreshold = 0.10f;
inline constexpr float kMaxPersonDetThreshold = 1.00f;

/** @brief 最小人脸像素尺寸门禁默认值 */
inline constexpr int kDefaultMinFaceSize = 32;
inline constexpr int kMinFaceSizeBound = 16;
inline constexpr int kMaxFaceSizeBound = 512;

/** @brief 纯数据逻辑人脸质量综合阈值默认值 */
inline constexpr float kDefaultMinFaceQuality = 0.40f;
inline constexpr float kMinFaceQualityBound = 0.00f;
inline constexpr float kMaxFaceQualityBound = 1.00f;

/** @brief 对齐后 RGB 脸块图像门禁开关默认值（默认关闭，需结合现场光照与镜头标定后开启） */
inline constexpr bool kDefaultEnableFaceImageGate = false;

/** @brief 对齐脸块清晰度下限默认值（占位值，必须结合现场采集标定；显式设上限防止 double->float 窄化溢出） */
inline constexpr float kDefaultMinFaceSharpness = 30.0f;
inline constexpr float kMinFaceSharpnessBound = 0.0f;
inline constexpr float kMaxFaceSharpnessBound = 100000.0f;

/** @brief 对齐脸块欠曝/过曝内部像素占比上限默认值（占位值；0.0 表示不允许任何暗/亮饱和像素） */
inline constexpr float kDefaultMaxFaceUnderexposure = 0.50f;
inline constexpr float kDefaultMaxFaceOverexposure = 0.30f;
inline constexpr float kMinFaceExposureBound = 0.0f;
inline constexpr float kMaxFaceExposureBound = 1.0f;

/** @brief 不同身份第一、第二名最小相似度分差默认值（0.0 表示关闭分差门禁） */
inline constexpr float kDefaultMinMatchMargin = 0.00f;
inline constexpr float kMinMatchMarginBound = 0.0f;
inline constexpr float kMaxMatchMarginBound = 1.0f;

/** @brief 跨帧特征融合开关默认值（默认开启） */
inline constexpr bool kDefaultEnableFaceFeatureFusion = true;

/** @brief 跨帧同人特征融合余弦相似度阈值默认值与合法域 */
inline constexpr float kDefaultFusionSimilarityThreshold = 0.62f;
inline constexpr float kMinFusionSimilarityThreshold = 0.45f;
inline constexpr float kMaxFusionSimilarityThreshold = 0.95f;

/** @brief 跨帧特征状态池未命中过期秒数默认值与合法域 */
inline constexpr float kDefaultFusionTtlSeconds = 3.0f;
inline constexpr float kMinFusionTtlSeconds = 0.5f;
inline constexpr float kMaxFusionTtlSeconds = 30.0f;

/** @brief 底库录入水平镜像 TTA 特征融合开关默认值（默认开启） */
inline constexpr bool kDefaultEnableEnrollFlipTta = true;
/** @brief 底库录入暗光自适应增强与无 EXIF 旋转重检兜底开关默认值（默认开启） */
inline constexpr bool kDefaultEnableEnrollImageEnhance = true;

/**
 * @brief 判断 JSON 拿到的 `double` 是否落在以 `float` 常量表示的闭区间内
 *
 * 判据是「舍入到 float 之后仍然不越界」而不是「double 直接跟 float 比」：JSON 里的 `0.95`
 * 是 double，而 `kMaxFusionSimilarityThreshold` 是 `0.949999988…`，直接比较会把**合法上界**
 * 判成越界（1.2e-8 的舍入差，症状是「schema 允许的边界值配不进去，静默回落默认值」）。
 *
 * 先做一次 double 域的粗筛再 `static_cast`：超出 float 表示范围的值直接窄化是 UB
 * （`min_face_sharpness: 1e100` 这类输入必须在这里被挡住，而不是靠调用方小心）。
 */
[[nodiscard]] inline bool within_float_bounds(double value, float min_bound, float max_bound) noexcept {
    if (!std::isfinite(value)) {
        return false;
    }
    const double lo = static_cast<double>(min_bound);
    const double hi = static_cast<double>(max_bound);
    if (value < lo - 1.0 || value > hi + 1.0) {
        return false;
    }
    const float narrowed = static_cast<float>(value);
    return narrowed >= min_bound && narrowed <= max_bound;
}

/** @brief 一次比对与推理实际生效的参数 */
struct MatchParams {
    float threshold{kDefaultMatchThreshold};
    size_t top_k{kDefaultMatchTopK};
    float face_det_threshold{kDefaultFaceDetThreshold};
    float person_det_threshold{kDefaultPersonDetThreshold};
    int min_face_size{kDefaultMinFaceSize};
    float min_face_quality{kDefaultMinFaceQuality};
    bool filter_unmatched{false};
    bool enable_face_image_gate{kDefaultEnableFaceImageGate};
    float min_face_sharpness{kDefaultMinFaceSharpness};
    float max_face_underexposure{kDefaultMaxFaceUnderexposure};
    float max_face_overexposure{kDefaultMaxFaceOverexposure};
    float min_match_margin{kDefaultMinMatchMargin};
    bool enable_face_feature_fusion{kDefaultEnableFaceFeatureFusion};
    float fusion_similarity_threshold{kDefaultFusionSimilarityThreshold};
    float fusion_ttl_seconds{kDefaultFusionTtlSeconds};
    bool enable_enroll_flip_tta{kDefaultEnableEnrollFlipTta};
    bool enable_enroll_image_enhance{kDefaultEnableEnrollImageEnhance};
};

namespace detail {

inline bool parse_env_double(const char* env_name, double& out_val) noexcept {
    if (const char* val = std::getenv(env_name); val != nullptr && val[0] != '\0') {
        char* end = nullptr;
        errno = 0;
        const double parsed = std::strtod(val, &end);
        if (end != val && errno != ERANGE && std::isfinite(parsed)) {
            out_val = parsed;
            return true;
        }
    }
    return false;
}

inline bool parse_env_bool(const char* env_name, bool& out_val) noexcept {
    if (const char* val = std::getenv(env_name); val != nullptr && val[0] != '\0') {
        if (std::strcmp(val, "1") == 0 || std::strcmp(val, "true") == 0 ||
            std::strcmp(val, "TRUE") == 0 || std::strcmp(val, "yes") == 0) {
            out_val = true;
            return true;
        }
        if (std::strcmp(val, "0") == 0 || std::strcmp(val, "false") == 0 ||
            std::strcmp(val, "FALSE") == 0 || std::strcmp(val, "no") == 0) {
            out_val = false;
            return true;
        }
    }
    return false;
}

} // namespace detail

/**
 * @brief 从运行环境（.env 文件或系统环境变量）加载默认算法配置
 *
 * 供算法插件在启动时覆盖编译期硬编码默认值；随后下发的进程级 --config 与任务级 JSON
 * 仍按原有优先级逐层覆盖。
 */
inline MatchParams load_match_params_from_env(MatchParams base = MatchParams{}) noexcept {
    MatchParams p = base;
    double dval = 0.0;
    bool bval = false;

    if (detail::parse_env_double("ZHULONG_FACE_MATCH_THRESHOLD", dval) &&
        dval >= static_cast<double>(kMinMatchThreshold) && dval <= static_cast<double>(kMaxMatchThreshold)) {
        p.threshold = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_FACE_TOP_K", dval) &&
        dval >= static_cast<double>(kMinMatchTopK) && dval <= static_cast<double>(kMaxMatchTopK)) {
        p.top_k = static_cast<size_t>(std::lround(dval));
    }
    if (detail::parse_env_double("ZHULONG_FACE_DET_THRESHOLD", dval) &&
        dval >= static_cast<double>(kMinFaceDetThreshold) && dval <= static_cast<double>(kMaxFaceDetThreshold)) {
        p.face_det_threshold = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_PERSON_DET_THRESHOLD", dval) &&
        dval >= static_cast<double>(kMinPersonDetThreshold) && dval <= static_cast<double>(kMaxPersonDetThreshold)) {
        p.person_det_threshold = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_MIN_FACE_SIZE", dval) &&
        dval >= static_cast<double>(kMinFaceSizeBound) && dval <= static_cast<double>(kMaxFaceSizeBound)) {
        p.min_face_size = static_cast<int>(std::lround(dval));
    }
    if (detail::parse_env_double("ZHULONG_MIN_FACE_QUALITY", dval) &&
        dval >= static_cast<double>(kMinFaceQualityBound) && dval <= static_cast<double>(kMaxFaceQualityBound)) {
        p.min_face_quality = static_cast<float>(dval);
    }
    if (detail::parse_env_bool("ZHULONG_FILTER_UNMATCHED", bval)) {
        p.filter_unmatched = bval;
    }
    if (detail::parse_env_bool("ZHULONG_ENABLE_FACE_IMAGE_GATE", bval)) {
        p.enable_face_image_gate = bval;
    }
    if (detail::parse_env_double("ZHULONG_MIN_FACE_SHARPNESS", dval) &&
        within_float_bounds(dval, kMinFaceSharpnessBound, kMaxFaceSharpnessBound)) {
        p.min_face_sharpness = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_MAX_FACE_UNDEREXPOSURE", dval) &&
        within_float_bounds(dval, kMinFaceExposureBound, kMaxFaceExposureBound)) {
        p.max_face_underexposure = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_MAX_FACE_OVEREXPOSURE", dval) &&
        within_float_bounds(dval, kMinFaceExposureBound, kMaxFaceExposureBound)) {
        p.max_face_overexposure = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_MIN_MATCH_MARGIN", dval) &&
        within_float_bounds(dval, kMinMatchMarginBound, kMaxMatchMarginBound)) {
        p.min_match_margin = static_cast<float>(dval);
    }
    if (detail::parse_env_bool("ZHULONG_ENABLE_FACE_FEATURE_FUSION", bval)) {
        p.enable_face_feature_fusion = bval;
    }
    if (detail::parse_env_double("ZHULONG_FUSION_SIMILARITY_THRESHOLD", dval) &&
        within_float_bounds(dval, kMinFusionSimilarityThreshold, kMaxFusionSimilarityThreshold)) {
        p.fusion_similarity_threshold = static_cast<float>(dval);
    }
    if (detail::parse_env_double("ZHULONG_FUSION_TTL_SECONDS", dval) &&
        within_float_bounds(dval, kMinFusionTtlSeconds, kMaxFusionTtlSeconds)) {
        p.fusion_ttl_seconds = static_cast<float>(dval);
    }
    if (detail::parse_env_bool("ZHULONG_ENABLE_ENROLL_FLIP_TTA", bval)) {
        p.enable_enroll_flip_tta = bval;
    }
    if (detail::parse_env_bool("ZHULONG_ENABLE_ENROLL_IMAGE_ENHANCE", bval)) {
        p.enable_enroll_image_enhance = bval;
    }

    return p;
}

/**
 * @brief 在扁平 JSON 里定位某个成员名后的冒号位置
 *
 * 只处理 `{"key": 1.5}` 这种形态（本项目的算法配置 schema 都是扁平数值），
 * 但键必须是**完整成员名**：引号包住的键名逐字相等，且前一个非空白字符是 `{` 或 `,`。
 * 按子串找 `"threshold"` 会命中 `"my_threshold"`，把别人的值当成自己的。
 *
 * 实现刻意不拼 `"\"" + key + "\""` 的 needle：本函数每帧、每路流都会被调用十余次，
 * 每次一个堆分配纯属浪费（解析结果只是几个标量）。
 *
 * @param from 从此偏移开始找（用于继续搜索同名成员的其他出现）
 * @return 冒号位置；未找到返回 npos
 */
inline size_t find_member_colon(std::string_view json, std::string_view key, size_t from = 0) {
    for (size_t pos = json.find('"', from); pos != std::string_view::npos; pos = json.find('"', pos + 1)) {
        const size_t name_begin = pos + 1;
        if (name_begin >= json.size() || json.size() - name_begin < key.size() + 1) {
            // 剩余长度连「键名 + 闭合引号」都放不下
            break;
        }
        if (json.compare(name_begin, key.size(), key) != 0) {
            continue;
        }
        if (json[name_begin + key.size()] != '"') {
            continue;
        }

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

        const size_t colon = json.find(':', name_begin + key.size() + 1);
        if (colon == std::string_view::npos) {
            continue;
        }
        return colon;
    }
    return std::string_view::npos;
}

inline bool is_json_delimiter(char ch) noexcept {
    return ch == ',' || ch == '}' || ch == ']' || static_cast<unsigned char>(ch) <= ' ';
}

/**
 * @brief 从扁平 JSON 对象里取一个数值字段
 *
 * @return true 表示找到并解析出有限数值
 */
inline bool find_json_number(std::string_view json, std::string_view key, double& out_value) {
    size_t from = 0;
    while (true) {
        const size_t colon = find_member_colon(json, key, from);
        if (colon == std::string_view::npos) {
            return false;
        }
        from = colon + 1;

        const size_t value_start = json.find_first_not_of(" \t\r\n", colon + 1);
        if (value_start == std::string_view::npos) {
            continue;
        }
        // 只截数值 token 交给 stod：拷贝落在 SSO 里（不像旧实现那样把整个 JSON 尾串拷贝一份）
        size_t value_end = value_start;
        while (value_end < json.size() &&
               std::string_view("+-0123456789.eE").find(json[value_end]) != std::string_view::npos) {
            ++value_end;
        }
        if (value_end == value_start) {
            continue; // 该键不是数值（布尔/字符串/null）：继续找同名成员的其他出现
        }
        try {
            size_t parsed = 0;
            const double value = std::stod(std::string(json.substr(value_start, value_end - value_start)), &parsed);
            if (parsed == 0 || !std::isfinite(value)) {
                continue;
            }
            out_value = value;
            return true;
        } catch (const std::exception&) {
            continue;
        }
    }
}

/**
 * @brief 从扁平 JSON 对象里取一个布尔字段
 */
inline bool find_json_bool(std::string_view json, std::string_view key, bool& out_value) {
    size_t from = 0;
    while (true) {
        const size_t colon = find_member_colon(json, key, from);
        if (colon == std::string_view::npos) {
            return false;
        }
        from = colon + 1;

        const size_t value_start = json.find_first_not_of(" \t\r\n", colon + 1);
        if (value_start == std::string_view::npos) {
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
}

/**
 * @brief 在默认值之上叠加任务级 JSON 的覆盖
 *
 * 全程按 `string_view` 扫原文：本函数在**每帧、每路流**的推理入口各调用一次
 * （见 `detect_scene_structure`），没有理由为它每帧拷贝整份配置。
 *
 * @param task_json 宿主下发的配置原文；空视图表示未下发，原样返回 defaults
 * @param defaults 进程级默认（来自 `init(config_json)`）
 * @return 生效参数；越界或非数值的字段保持 defaults 对应项
 *
 * @note 越界回落而不是报错：宿主的配置 schema 已经校验过一遍，这里再兜一层是因为
 *       包可能比宿主的 schema 新/旧（分开交付）。宁可退化也不能让插件拒绝工作。
 */
inline MatchParams resolve_match_params(std::string_view task_json, MatchParams defaults) {
    if (task_json.empty()) {
        return defaults;
    }

    MatchParams resolved = defaults;
    const std::string_view json = task_json;

    // 1. 比对阈值 (支持 threshold 与别名 match_threshold)
    double threshold = 0.0;
    if ((find_json_number(json, "threshold", threshold) || find_json_number(json, "match_threshold", threshold)) &&
        threshold >= static_cast<double>(kMinMatchThreshold) &&
        threshold <= static_cast<double>(kMaxMatchThreshold)) {
        resolved.threshold = static_cast<float>(threshold);
    }

    // 2. Top-K
    double top_k = 0.0;
    if (find_json_number(json, "top_k", top_k) && top_k >= static_cast<double>(kMinMatchTopK) &&
        top_k <= static_cast<double>(kMaxMatchTopK)) {
        resolved.top_k = static_cast<size_t>(std::lround(top_k));
    }

    // 3. 人脸检测置信度 (支持 face_det_threshold 与别名 det_threshold)
    double face_det = 0.0;
    if ((find_json_number(json, "face_det_threshold", face_det) || find_json_number(json, "det_threshold", face_det)) &&
        face_det >= static_cast<double>(kMinFaceDetThreshold) &&
        face_det <= static_cast<double>(kMaxFaceDetThreshold)) {
        resolved.face_det_threshold = static_cast<float>(face_det);
    }

    // 4. 人体检测置信度
    double person_det = 0.0;
    if (find_json_number(json, "person_det_threshold", person_det) &&
        person_det >= static_cast<double>(kMinPersonDetThreshold) &&
        person_det <= static_cast<double>(kMaxPersonDetThreshold)) {
        resolved.person_det_threshold = static_cast<float>(person_det);
    }

    // 5. 最小人脸像素尺寸
    double min_face_size = 0.0;
    if (find_json_number(json, "min_face_size", min_face_size) &&
        min_face_size >= static_cast<double>(kMinFaceSizeBound) &&
        min_face_size <= static_cast<double>(kMaxFaceSizeBound)) {
        resolved.min_face_size = static_cast<int>(std::lround(min_face_size));
    }

    // 6. 人脸质量综合阈值 (支持 min_face_quality 与别名 face_quality_threshold)
    double min_face_quality = 0.0;
    if ((find_json_number(json, "min_face_quality", min_face_quality) || find_json_number(json, "face_quality_threshold", min_face_quality)) &&
        min_face_quality >= static_cast<double>(kMinFaceQualityBound) &&
        min_face_quality <= static_cast<double>(kMaxFaceQualityBound)) {
        resolved.min_face_quality = static_cast<float>(min_face_quality);
    }

    // 7. 过滤未识别目标开关
    bool filter_unmatched = false;
    if (find_json_bool(json, "filter_unmatched", filter_unmatched)) {
        resolved.filter_unmatched = filter_unmatched;
    }

    // 8. 对齐脸块图像门禁开关（单一关闭语义）
    bool enable_face_image_gate = false;
    if (find_json_bool(json, "enable_face_image_gate", enable_face_image_gate)) {
        resolved.enable_face_image_gate = enable_face_image_gate;
    }

    // 9. 对齐脸块清晰度下限（有界区间，阻断 double -> float 窄化溢出 UB）
    double min_face_sharpness = 0.0;
    if (find_json_number(json, "min_face_sharpness", min_face_sharpness) &&
        within_float_bounds(min_face_sharpness, kMinFaceSharpnessBound, kMaxFaceSharpnessBound)) {
        resolved.min_face_sharpness = static_cast<float>(min_face_sharpness);
    }

    // 10. 对齐脸块欠曝像素占比上限 [0.0, 1.0]
    double max_face_underexposure = 0.0;
    if (find_json_number(json, "max_face_underexposure", max_face_underexposure) &&
        within_float_bounds(max_face_underexposure, kMinFaceExposureBound, kMaxFaceExposureBound)) {
        resolved.max_face_underexposure = static_cast<float>(max_face_underexposure);
    }

    // 11. 对齐脸块过曝像素占比上限 [0.0, 1.0]
    double max_face_overexposure = 0.0;
    if (find_json_number(json, "max_face_overexposure", max_face_overexposure) &&
        within_float_bounds(max_face_overexposure, kMinFaceExposureBound, kMaxFaceExposureBound)) {
        resolved.max_face_overexposure = static_cast<float>(max_face_overexposure);
    }

    // 12. 不同身份第一、第二名最小相似度分差 [0.0, 1.0]
    double min_match_margin = 0.0;
    if (find_json_number(json, "min_match_margin", min_match_margin) &&
        within_float_bounds(min_match_margin, kMinMatchMarginBound, kMaxMatchMarginBound)) {
        resolved.min_match_margin = static_cast<float>(min_match_margin);
    }

    // 13. 跨帧特征融合开关
    bool enable_face_feature_fusion = true;
    if (find_json_bool(json, "enable_face_feature_fusion", enable_face_feature_fusion)) {
        resolved.enable_face_feature_fusion = enable_face_feature_fusion;
    }

    // 14. 跨帧同人特征融合相似度阈值 [kMinFusionSimilarityThreshold, kMaxFusionSimilarityThreshold]
    //     区间边界取自常量本身（而不是再写一遍字面量），避免改常量忘了改解析、两处悄悄漂移
    double fusion_similarity_threshold = 0.0;
    if (find_json_number(json, "fusion_similarity_threshold", fusion_similarity_threshold) &&
        within_float_bounds(fusion_similarity_threshold, kMinFusionSimilarityThreshold,
                            kMaxFusionSimilarityThreshold)) {
        resolved.fusion_similarity_threshold = static_cast<float>(fusion_similarity_threshold);
    }

    // 15. 跨帧特征状态池过期秒数 [kMinFusionTtlSeconds, kMaxFusionTtlSeconds]
    double fusion_ttl_seconds = 0.0;
    if (find_json_number(json, "fusion_ttl_seconds", fusion_ttl_seconds) &&
        within_float_bounds(fusion_ttl_seconds, kMinFusionTtlSeconds, kMaxFusionTtlSeconds)) {
        resolved.fusion_ttl_seconds = static_cast<float>(fusion_ttl_seconds);
    }

    // 16. 底库录入水平镜像 TTA 开关
    bool enable_enroll_flip_tta = true;
    if (find_json_bool(json, "enable_enroll_flip_tta", enable_enroll_flip_tta)) {
        resolved.enable_enroll_flip_tta = enable_enroll_flip_tta;
    }

    // 17. 底库录入暗光增强与旋转兜底开关
    bool enable_enroll_image_enhance = true;
    if (find_json_bool(json, "enable_enroll_image_enhance", enable_enroll_image_enhance)) {
        resolved.enable_enroll_image_enhance = enable_enroll_image_enhance;
    }

    return resolved;
}

} // namespace zhulong::face
