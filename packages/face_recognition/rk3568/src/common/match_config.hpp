/**
 * @file match_config.hpp
 * @brief 1:N 比对参数（阈值 / Top-K）的默认值与扁平 JSON 解析
 *
 * 为什么单独抽出来：同一套参数有**两个来源**——进程级（`--config` / `init`）与
 * 任务级（宿主逐路流下发的 `MSG_TASK_CONFIG`），两者的取值范围、缺省回落语义必须完全
 * 一致。写两遍的结局是「改了一个忘了另一个」，而症状只是「某一路流的阈值不对」。
 *
 * 另外它把这段逻辑变成**不需要模型**就能单测的纯函数（`make unit-test` 覆盖）：
 * 阈值解析错了不会崩、只会让识别结果对不上配置，靠端到端测试几乎抓不住。
 */

#pragma once

#include <cmath>
#include <cstddef>
#include <exception>
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

/** @brief 一次比对实际生效的参数 */
struct MatchParams {
    float threshold{kDefaultMatchThreshold};
    size_t top_k{kDefaultMatchTopK};
};

/**
 * @brief 从扁平 JSON 对象里取一个数值字段
 *
 * 只处理 `{"key": 1.5}` 这种形态（本项目的算法配置 schema 都是扁平数值），
 * 但键必须是**完整成员名**：前一个非空白字符是 `{` 或 `,`。
 * 按子串找 `"threshold"` 会命中 `"my_threshold"`，把别人的值当成自己的。
 *
 * @return true 表示找到并解析出有限数值
 */
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
            // 该键不是数值（布尔/字符串）：继续找同名成员的其他出现
            continue;
        }
    }
    return false;
}

/**
 * @brief 在默认值之上叠加任务级 JSON 的覆盖
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
    const std::string json(task_json);

    double threshold = 0.0;
    if (find_json_number(json, "threshold", threshold) &&
        threshold >= static_cast<double>(kMinMatchThreshold) &&
        threshold <= static_cast<double>(kMaxMatchThreshold)) {
        resolved.threshold = static_cast<float>(threshold);
    }

    double top_k = 0.0;
    if (find_json_number(json, "top_k", top_k) && top_k >= static_cast<double>(kMinMatchTopK) &&
        top_k <= static_cast<double>(kMaxMatchTopK)) {
        resolved.top_k = static_cast<size_t>(std::lround(top_k));
    }

    return resolved;
}

} // namespace zhulong::face
