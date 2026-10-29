/**
 * @file plate_format.hpp
 * @brief 车牌号码结构校验（纯函数，可被单测直接钉住）
 *
 * ⚠️ 本校验**无条件执行**，不提供开关：号码结构不合法 ⇒ 这条结果在下游没有任何
 *    语义（宿主的抓拍去重键就是号码本身：`cap:<camera>:vehicle:plate:<号码>`），
 *    保留它只会污染证据库。CTC 贪心解码对输出长度没有上限（21 个时间步），
 *    泥牌/遮挡/夜间噪点很容易解出一串「看着像号码」的垃圾。
 *
 *    想对比「校验到底挡掉了什么」不需要配置：打开 ZHULONG_PLATE_DEBUG=1 就能看到
 *    每条被丢弃的号码与原因。
 *
 * 设计约束：
 *   - 纯函数、零第三方依赖（UTF-8 自行解码，只比较码点）；
 *   - 字符集来自 plate_charset.hpp（与解码器同一份表，不另抄一份）；
 *   - 规则刻意保守：只做「结构上不可能是真牌」的判定，不做地区/连号等语义校验。
 */

#pragma once

#include <cstdint>
#include <string>

namespace zhulong::plate {

/** @brief 校验失败原因（用于调试输出与单测断言，不跨 C ABI） */
enum class PlateFormatProblem : int32_t {
    None = 0,            /**< 通过 */
    InvalidUtf8,         /**< 号码不是合法 UTF-8（不该发生，说明上游串码） */
    BadLength,           /**< 码点数不在 7~8 */
    BadProvince,         /**< 首字符不是省级简称（也不是使馆/领馆前缀） */
    BadAuthority,        /**< 第 2 位不是发牌机关字母（A-Z 去 I/O） */
    BadCharacter,        /**< 序号位出现非字母数字（且不是合法特殊尾字） */
    BadNewEnergyLayout,  /**< 8 位但既无新能源 D/F 也不是特殊尾字牌 */
};

/** @brief 校验结论 */
struct PlateFormatVerdict {
    bool valid{false};
    PlateFormatProblem problem{PlateFormatProblem::None};
};

/**
 * @brief 校验车牌号码结构
 *
 * 规则（7 位普通牌 / 8 位新能源·挂车牌 / 使领馆牌）：
 *   - 码点数 ∈ {7, 8}；
 *   - 首字符为 31 个省级简称之一，第 2 位为发牌机关字母（A-Z 去 I/O）；
 *   - 序号位只允许数字与字母（去 I/O），末位允许特殊尾字（学警港澳挂使领民航危）；
 *   - 8 位必须满足其一：第 3 位 D/F（小型新能源）、末位 D/F（大型新能源）、末位特殊尾字（挂车等）；
 *   - 使馆/领馆牌：使|领 + 6 位数字。
 *
 * @param plate_number CTC 解码出的号码（UTF-8）
 */
PlateFormatVerdict validate_plate_number(const std::string& plate_number);

/** @brief 失败原因的稳定短名（stderr 调试输出与单测用） */
const char* plate_format_problem_name(PlateFormatProblem problem);

} // namespace zhulong::plate
