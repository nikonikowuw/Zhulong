/**
 * @file plate_charset.hpp
 * @brief 车牌字符表、类别索引分区与颜色表（识别模型类别顺序的单一来源）
 *
 * ⚠️ 顺序即模型类别顺序，改动会导致全线串码。必须与训练侧（以及参考实现
 *    scripts/test_infer.py 的 PLATE_NAMES / COLOR_NAMES）逐字节一致。
 *
 * 本头文件同时被两处使用：
 *   - plate_recognizer.cpp：CTC 贪心解码（类别索引 → 字符）
 *   - plate_format.cpp：号码结构校验（省简称集合、特殊尾字集合）
 * 两处各抄一份字符表迟早会漂移，而漂移的表现是「能跑但号码/校验全错」，
 * 因此分区由下面的索引常量显式声明，并用 static_assert 钉住边界字符。
 */

#pragma once

#include <cstddef>
#include <iterator>
#include <string_view>

namespace zhulong::plate {

/**
 * @brief 车牌号码字符表（CTC 类别索引，0 为空白符）
 *
 * 分区（模型类别顺序决定，非人为约定）：
 *   [0]       空白符
 *   [1, 31]   31 个省级简称（京…新）
 *   [32, 41]  特殊尾字（学 警 港 澳 挂 使 领 民 航 危）
 *   [42, 51]  数字 0-9
 *   [52, 75]  字母 A-Z（不含 I/O，号码中不使用）
 *   [76, 77]  额外尾字（险 品）
 *
 * ⚠️ 尾字分区（[32,41] ∪ [76,77]）是**结构校验的合法末位**。模型能解出某个类目、
 *    而校验永远否决它，等于把训练集里真实存在的类目静默丢掉（少报一块真牌，且
 *    除 ZHULONG_PLATE_DEBUG 外没有任何信号）。所以字符表扩容时，务必把新增的尾字
 *    放进 [kFirstExtraSuffixIndex, kLastExtraSuffixIndex]，两处同源。
 */
inline constexpr const char* kPlateNames[] = {
    "#", "京", "沪", "津", "渝", "冀", "晋", "蒙", "辽", "吉", "黑",
    "苏", "浙", "皖", "闽", "赣", "鲁", "豫", "鄂", "湘", "粤",
    "桂", "琼", "川", "贵", "云", "藏", "陕", "甘", "青", "宁",
    "新", "学", "警", "港", "澳", "挂", "使", "领", "民", "航", "危",
    "0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
    "A", "B", "C", "D", "E", "F", "G", "H", "J", "K",
    "L", "M", "N", "P", "Q", "R", "S", "T", "U", "V",
    "W", "X", "Y", "Z", "险", "品"
};

inline constexpr std::size_t kNumPlateChars = std::size(kPlateNames);

/** @brief 空白符类别索引（CTC 解码时跳过） */
inline constexpr std::size_t kBlankCharIndex = 0;
/** @brief 省级简称区间 [kFirstProvinceIndex, kLastProvinceIndex] */
inline constexpr std::size_t kFirstProvinceIndex = 1;
inline constexpr std::size_t kLastProvinceIndex = 31;
/** @brief 特殊尾字区间 [kFirstSpecialIndex, kLastSpecialIndex] */
inline constexpr std::size_t kFirstSpecialIndex = 32;
inline constexpr std::size_t kLastSpecialIndex = 41;
/** @brief 额外尾字区间 [kFirstExtraSuffixIndex, kLastExtraSuffixIndex]（训练集扩展的合法尾字） */
inline constexpr std::size_t kFirstExtraSuffixIndex = 76;
inline constexpr std::size_t kLastExtraSuffixIndex = 77;

static_assert(std::string_view{kPlateNames[kBlankCharIndex]} == "#");
static_assert(std::string_view{kPlateNames[kFirstProvinceIndex]} == "京");
static_assert(std::string_view{kPlateNames[kLastProvinceIndex]} == "新");
static_assert(std::string_view{kPlateNames[kFirstSpecialIndex]} == "学");
static_assert(std::string_view{kPlateNames[kLastSpecialIndex]} == "危");
// 分区之后必须是数字 0，否则说明有人往表中间插了字符而没更新分区常量
static_assert(std::string_view{kPlateNames[kLastSpecialIndex + 1]} == "0");
static_assert(std::string_view{kPlateNames[kFirstExtraSuffixIndex]} == "险");
static_assert(std::string_view{kPlateNames[kLastExtraSuffixIndex]} == "品");
// 额外尾字区间必须一直延伸到表尾：往后追加字符会同时改变「合法尾字」的语义
static_assert(kLastExtraSuffixIndex + 1 == kNumPlateChars);

/** @brief 颜色分类类别表（顺序即模型类别顺序） */
inline constexpr const char* kColorNames[] = {"黑色", "蓝色", "绿色", "白色", "黄色"};
inline constexpr std::size_t kNumColors = std::size(kColorNames);

/** @brief 颜色类别索引（与 kColorNames 顺序一致） */
inline constexpr std::size_t kColorBlackIndex = 0;
inline constexpr std::size_t kColorBlueIndex = 1;
inline constexpr std::size_t kColorGreenIndex = 2;
inline constexpr std::size_t kColorWhiteIndex = 3;
inline constexpr std::size_t kColorYellowIndex = 4;

static_assert(std::string_view{kColorNames[kColorBlackIndex]} == "黑色");
static_assert(std::string_view{kColorNames[kColorBlueIndex]} == "蓝色");
static_assert(std::string_view{kColorNames[kColorGreenIndex]} == "绿色");
static_assert(std::string_view{kColorNames[kColorWhiteIndex]} == "白色");
static_assert(std::string_view{kColorNames[kColorYellowIndex]} == "黄色");

} // namespace zhulong::plate
