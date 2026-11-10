#include "plate_format.hpp"

#include "common/plate_charset.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <string_view>
#include <vector>

namespace zhulong::plate {

namespace {

/** @brief 号牌码点数下界（普通 7 位 / 使馆 7 位 / 警学 7 位） */
constexpr std::size_t kMinPlateCodepoints = 7;
/** @brief 号牌码点数上界（新能源 8 位 / 挂车 8 位） */
constexpr std::size_t kMaxPlateCodepoints = 8;
/** @brief 使馆/领馆号牌的序号位数（使 + 6 位数字） */
constexpr std::size_t kEnvoySerialDigits = 6;

/**
 * @brief 解码 UTF-8 为码点序列
 *
 * 拒绝被截断的序列、非法的续字节、**过长编码（overlong）**、代理区码点与超出
 * Unicode 范围的码点。码点级别比较是本文件唯一可靠的字符比较方式（汉字 3 字节，
 * 逐字节比较既难读又容易踩子串误匹配）。
 *
 * overlong 必须在这里挡掉：`C0 80` 与 `00` 都是 U+0000，若放它过去，后面所有
 * 「首字符是省简称 / 第 2 位是字母」的判断都建立在一个被错误编码的字节串上。
 */
bool decode_utf8(std::string_view text, std::vector<uint32_t>& codepoints) {
    codepoints.clear();
    codepoints.reserve(text.size());

    std::size_t index = 0;
    while (index < text.size()) {
        const unsigned char lead = static_cast<unsigned char>(text[index]);
        uint32_t codepoint = 0;
        std::size_t continuation = 0;
        uint32_t min_codepoint = 0; // 该长度的最短合法编码，用于拦 overlong
        if (lead < 0x80) {
            codepoint = lead;
        } else if ((lead & 0xE0) == 0xC0) {
            codepoint = lead & 0x1FU;
            continuation = 1;
            min_codepoint = 0x80;
        } else if ((lead & 0xF0) == 0xE0) {
            codepoint = lead & 0x0FU;
            continuation = 2;
            min_codepoint = 0x800;
        } else if ((lead & 0xF8) == 0xF0) {
            codepoint = lead & 0x07U;
            continuation = 3;
            min_codepoint = 0x10000;
        } else {
            return false;
        }
        if (index + continuation >= text.size()) {
            return false;
        }
        for (std::size_t k = 1; k <= continuation; ++k) {
            const unsigned char next = static_cast<unsigned char>(text[index + k]);
            if ((next & 0xC0) != 0x80) {
                return false;
            }
            codepoint = (codepoint << 6) | (next & 0x3FU);
        }
        if (codepoint < min_codepoint || codepoint > 0x10FFFF ||
            (codepoint >= 0xD800 && codepoint <= 0xDFFF)) {
            return false;
        }
        codepoints.push_back(codepoint);
        index += continuation + 1;
    }
    return true;
}

/** @brief 单个 UTF-8 字符串 → 码点；空串或非法返回 0 */
uint32_t codepoint_of(std::string_view utf8) {
    std::vector<uint32_t> codepoints;
    if (utf8.empty() || !decode_utf8(utf8, codepoints) || codepoints.size() != 1) {
        return 0;
    }
    return codepoints.front();
}

/** @brief 从字符表区间取出码点集合（省份 / 尾字） */
std::vector<uint32_t> codepoints_in_range(std::size_t first, std::size_t last) {
    std::vector<uint32_t> result;
    if (last >= kNumPlateChars || first > last) {
        return result;
    }
    result.reserve(last - first + 1);
    for (std::size_t i = first; i <= last; ++i) {
        const uint32_t codepoint = codepoint_of(kPlateNames[i]);
        if (codepoint != 0) {
            result.push_back(codepoint);
        }
    }
    return result;
}

const std::vector<uint32_t>& province_codepoints() {
    static const std::vector<uint32_t> values = [] {
        auto list = codepoints_in_range(kFirstProvinceIndex, kLastProvinceIndex);
        std::sort(list.begin(), list.end());
        return list;
    }();
    return values;
}

/**
 * @brief 合法尾字码点集合（特殊尾字 ∪ 额外尾字，已排序便于二分查找）
 *
 * 两段都来自 `plate_charset.hpp`，不另抄一份字面量：字符表扩容而校验没跟上，
 * 表现是「模型认得、永远报不出来」，且不会编译失败。
 */
const std::vector<uint32_t>& suffix_codepoints() {
    static const std::vector<uint32_t> values = [] {
        std::vector<uint32_t> merged = codepoints_in_range(kFirstSpecialIndex, kLastSpecialIndex);
        const std::vector<uint32_t> extra = codepoints_in_range(kFirstExtraSuffixIndex, kLastExtraSuffixIndex);
        merged.insert(merged.end(), extra.begin(), extra.end());
        std::sort(merged.begin(), merged.end());
        return merged;
    }();
    return values;
}

/** @brief 在字符表某个区间里按字面量找码点（0 表示表里没有该字） */
uint32_t codepoint_in_range(std::string_view name, std::size_t first, std::size_t last) {
    for (std::size_t i = first; i <= last && i < kNumPlateChars; ++i) {
        if (kPlateNames[i] == name) {
            return codepoint_of(kPlateNames[i]);
        }
    }
    return 0;
}

bool contains(const std::vector<uint32_t>& sorted_set, uint32_t codepoint) noexcept {
    return std::binary_search(sorted_set.begin(), sorted_set.end(), codepoint);
}

constexpr bool is_digit(uint32_t codepoint) noexcept { return codepoint >= '0' && codepoint <= '9'; }

/** @brief 号码中允许出现的字母：A-Z 去 I/O（I/O 与数字 1/0 易混，标准号牌不用） */
constexpr bool is_sequence_letter(uint32_t codepoint) noexcept {
    if (codepoint < 'A' || codepoint > 'Z') {
        return false;
    }
    return codepoint != 'I' && codepoint != 'O';
}

/** @brief 新能源号牌的 D/F 标识位 */
constexpr bool is_new_energy_letter(uint32_t codepoint) noexcept { return codepoint == 'D' || codepoint == 'F'; }

/** @brief 使馆/领馆号牌：使|领 + 6 位数字（黑底白字，首字符即特殊前缀） */
bool is_envoy_plate(const std::vector<uint32_t>& codepoints) {
    static const uint32_t kEnvoyLead = codepoint_in_range("使", kFirstSpecialIndex, kLastSpecialIndex);
    static const uint32_t kConsulateLead = codepoint_in_range("领", kFirstSpecialIndex, kLastSpecialIndex);

    if (codepoints.size() != kEnvoySerialDigits + 1) {
        return false;
    }
    if (kEnvoyLead == 0 || kConsulateLead == 0) {
        return false; // 字符表里没有使/领，无法判定，交给通用规则
    }
    if (codepoints.front() != kEnvoyLead && codepoints.front() != kConsulateLead) {
        return false;
    }
    for (std::size_t i = 1; i < codepoints.size(); ++i) {
        if (!is_digit(codepoints[i])) {
            return false;
        }
    }
    return true;
}

/**
 * @brief 号码结构规则（无条件执行，不可关闭）
 *
 * 通过返回 None，否则返回第一个命中的失败原因。顺序有意为之：先定字段角色
 * （省简称、发牌机关），再定序号字符集，最后才是位数与新能源布局——这样
 * 单测的失败原因能区分「读错字」与「多/少一位」。
 */
PlateFormatProblem check_structure(const std::vector<uint32_t>& codepoints) {
    if (is_envoy_plate(codepoints)) {
        return PlateFormatProblem::None;
    }

    if (!contains(province_codepoints(), codepoints[0])) {
        return PlateFormatProblem::BadProvince;
    }
    if (!is_sequence_letter(codepoints[1])) {
        return PlateFormatProblem::BadAuthority;
    }

    for (std::size_t i = 2; i < codepoints.size(); ++i) {
        const bool is_last = (i + 1 == codepoints.size());
        if (is_digit(codepoints[i]) || is_sequence_letter(codepoints[i])) {
            continue;
        }
        if (is_last && contains(suffix_codepoints(), codepoints[i])) {
            continue;
        }
        return PlateFormatProblem::BadCharacter;
    }

    // 8 位只可能是三种：小型新能源（第 3 位 D/F）、大型新能源（末位 D/F）、挂车等尾字牌
    if (codepoints.size() == kMaxPlateCodepoints) {
        const bool third_is_flag = is_new_energy_letter(codepoints[2]);
        const bool last_is_flag = is_new_energy_letter(codepoints.back());
        const bool last_is_suffix = contains(suffix_codepoints(), codepoints.back());
        if (!third_is_flag && !last_is_flag && !last_is_suffix) {
            return PlateFormatProblem::BadNewEnergyLayout;
        }
    }

    return PlateFormatProblem::None;
}

} // namespace

PlateFormatVerdict validate_plate_number(std::string_view plate_number) {
    PlateFormatVerdict verdict;

    std::vector<uint32_t> codepoints;
    if (!decode_utf8(plate_number, codepoints)) {
        verdict.problem = PlateFormatProblem::InvalidUtf8;
        return verdict;
    }
    if (codepoints.size() < kMinPlateCodepoints || codepoints.size() > kMaxPlateCodepoints) {
        verdict.problem = PlateFormatProblem::BadLength;
        return verdict;
    }

    verdict.problem = check_structure(codepoints);
    verdict.valid = (verdict.problem == PlateFormatProblem::None);
    return verdict;
}

} // namespace zhulong::plate
