#include "plate_format.hpp"

#include "common/plate_charset.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstring>
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
 * 只做「结构上合法」的判定：拒绝被截断的序列、非法的续字节与超出 Unicode
 * 范围的码点。码点级别比较是本文件唯一可靠的字符比较方式（汉字 3 字节，
 * 逐字节比较既难读又容易踩子串误匹配）。
 */
bool decode_utf8(const std::string& text, std::vector<uint32_t>& codepoints) {
    codepoints.clear();
    codepoints.reserve(text.size());

    std::size_t index = 0;
    while (index < text.size()) {
        const unsigned char lead = static_cast<unsigned char>(text[index]);
        uint32_t codepoint = 0;
        std::size_t continuation = 0;
        if (lead < 0x80) {
            codepoint = lead;
        } else if ((lead & 0xE0) == 0xC0) {
            codepoint = lead & 0x1FU;
            continuation = 1;
        } else if ((lead & 0xF0) == 0xE0) {
            codepoint = lead & 0x0FU;
            continuation = 2;
        } else if ((lead & 0xF8) == 0xF0) {
            codepoint = lead & 0x07U;
            continuation = 3;
        } else {
            return false;
        }
        if (index + continuation >= text.size() && continuation != 0) {
            return false;
        }
        for (std::size_t k = 1; k <= continuation; ++k) {
            const unsigned char next = static_cast<unsigned char>(text[index + k]);
            if ((next & 0xC0) != 0x80) {
                return false;
            }
            codepoint = (codepoint << 6) | (next & 0x3FU);
        }
        if (codepoint > 0x10FFFF || (codepoint >= 0xD800 && codepoint <= 0xDFFF)) {
            return false;
        }
        codepoints.push_back(codepoint);
        index += continuation + 1;
    }
    return true;
}

/** @brief 单个 UTF-8 字符串 → 码点；空串或非法返回 0 */
uint32_t codepoint_of(const char* utf8) {
    std::vector<uint32_t> codepoints;
    if (utf8 == nullptr || !decode_utf8(utf8, codepoints) || codepoints.size() != 1) {
        return 0;
    }
    return codepoints.front();
}

/** @brief 从字符表区间取出码点集合（省份 / 特殊尾字） */
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
    static const std::vector<uint32_t> values =
        codepoints_in_range(kFirstProvinceIndex, kLastProvinceIndex);
    return values;
}

const std::vector<uint32_t>& special_codepoints() {
    static const std::vector<uint32_t> values =
        codepoints_in_range(kFirstSpecialIndex, kLastSpecialIndex);
    return values;
}

/** @brief 在字符表某个区间里按字面量找码点（0 表示表里没有该字） */
uint32_t codepoint_in_range(const char* name, std::size_t first, std::size_t last) {
    for (std::size_t i = first; i <= last && i < kNumPlateChars; ++i) {
        if (std::strcmp(kPlateNames[i], name) == 0) {
            return codepoint_of(kPlateNames[i]);
        }
    }
    return 0;
}

bool contains(const std::vector<uint32_t>& set, uint32_t codepoint) {
    return std::find(set.begin(), set.end(), codepoint) != set.end();
}

bool is_digit(uint32_t codepoint) { return codepoint >= '0' && codepoint <= '9'; }

/** @brief 号码中允许出现的字母：A-Z 去 I/O（I/O 与数字 1/0 易混，标准号牌不用） */
bool is_sequence_letter(uint32_t codepoint) {
    if (codepoint < 'A' || codepoint > 'Z') {
        return false;
    }
    return codepoint != 'I' && codepoint != 'O';
}

/** @brief 新能源号牌的 D/F 标识位 */
bool is_new_energy_letter(uint32_t codepoint) { return codepoint == 'D' || codepoint == 'F'; }

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
        if (is_last && contains(special_codepoints(), codepoints[i])) {
            continue;
        }
        return PlateFormatProblem::BadCharacter;
    }

    // 8 位只可能是三种：小型新能源（第 3 位 D/F）、大型新能源（末位 D/F）、挂车等特殊尾字牌
    if (codepoints.size() == kMaxPlateCodepoints) {
        const bool third_is_flag = is_new_energy_letter(codepoints[2]);
        const bool last_is_flag = is_new_energy_letter(codepoints.back());
        const bool last_is_special = contains(special_codepoints(), codepoints.back());
        if (!third_is_flag && !last_is_flag && !last_is_special) {
            return PlateFormatProblem::BadNewEnergyLayout;
        }
    }

    return PlateFormatProblem::None;
}

} // namespace

PlateFormatVerdict validate_plate_number(const std::string& plate_number) {
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

const char* plate_format_problem_name(PlateFormatProblem problem) {
    switch (problem) {
    case PlateFormatProblem::None:
        return "none";
    case PlateFormatProblem::InvalidUtf8:
        return "invalid_utf8";
    case PlateFormatProblem::BadLength:
        return "bad_length";
    case PlateFormatProblem::BadProvince:
        return "bad_province";
    case PlateFormatProblem::BadAuthority:
        return "bad_authority";
    case PlateFormatProblem::BadCharacter:
        return "bad_character";
    case PlateFormatProblem::BadNewEnergyLayout:
        return "bad_new_energy_layout";
    }
    return "unknown";
}

} // namespace zhulong::plate
