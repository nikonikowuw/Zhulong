/**
 * @file face_attribute.hpp
 * @brief 人脸属性（性别 + 年龄段）分类器：采样常量、标签语义与模型输出解释
 *
 * 与人体属性（person_attribute.hpp）是两条互不相干的通道：
 *   - 人体属性吃**人体框**（192×256 拉伸），由 PULC PPLCNet 多标签模型产出 26 个
 *     独立 sigmoid，标签挂在 `zhulong_person_item::attributes`；
 *   - 人脸属性吃**已对齐的人脸**（96×96 相似变换 warp），由 InsightFace
 *     `genderage.onnx` 产出性别 softmax + 年龄回归，标签挂在 `zhulong_face_item::attributes`。
 *
 * 两者共用 `zhulong::PersonAttribute` 作为元素类型（同一个 C ABI 布局），只有标签名不同。
 */

#ifndef ZHULONG_FACE_RECOGNITION_FACE_ATTRIBUTE_HPP
#define ZHULONG_FACE_RECOGNITION_FACE_ATTRIBUTE_HPP

#include "common/face_types.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace zhulong::face {

/** @brief 人脸属性模型输入边长（genderage 是方形 96×96 输入） */
constexpr int kFaceAttributeInputWidth = 96;
constexpr int kFaceAttributeInputHeight = 96;

/** @brief 模型输出张量元素数：性别 logits 2 维、年龄回归 1 维（SPLIT_OUTPUT 布局） */
constexpr size_t kFaceAttributeGenderCount = 2;
constexpr size_t kFaceAttributeAgeCount = 1;

/** @brief 年龄区间个数；标签见 `face_attribute_age_label` */
constexpr size_t kFaceAttributeAgeBucketCount = 5;

/** @brief 年龄回归值的可信上界（`age_raw` 被模型钳在 [0, 1]，×100 后不应超过它） */
constexpr float kFaceAttributeAgeMaxYears = 100.0f;

/** @brief 性别标签（argmax 结果，互斥，至多一条） */
inline constexpr const char* kFaceAttributeFemaleLabel = "Female";
inline constexpr const char* kFaceAttributeMaleLabel = "Male";

/**
 * @brief 性别 softmax 置信度阈值
 *
 * 低于它时性别属性整条不输出（而不是硬取 argmax 再给个 0.51）：两个 logits 接近时
 * 模型其实没有判断，输出一个过半概率只会让下游当成结论。
 */
constexpr float kFaceAttributeGenderThreshold = 0.60f;

/**
 * @brief 年龄段属性固定输出的分数
 *
 * 年龄是回归而不是分类，模型没有给出「落在该区间的概率」。用性别置信度冒充它是
 * 编造数据；固定 1.0 的语义是「该区间被选中」，下游按标签取值、按性别项判断权重。
 */
constexpr float kFaceAttributeAgeSelectedScore = 1.0f;

/**
 * @brief 各年龄区间的**下界**（单位：年），长度等于区间数
 *
 * 模型输出的 `age × 100` 只当作**区间定位**用，不当作绝对年龄：实测与真实年龄的
 * 相关系数接近 0（SCface 20 张证件照 corr ≈ -0.04、MAE 3.6 年），单点数值不可信，
 * 但落在哪个十年段是可信的。因此对外只暴露区间标签，不暴露裸数值。
 *
 * 最后一档是开区间（`Age60+`），所以这里只存下界、不存上界 —— 上界表会让最后一档
 * 的边界值变成永远走不到的兜底分支。
 */
constexpr std::array<float, kFaceAttributeAgeBucketCount> kFaceAttributeAgeBucketStarts{
    0.0f, 18.0f, 30.0f, 45.0f, 60.0f,
};

/**
 * @brief 年龄段标签；下标超出 [0, kFaceAttributeAgeBucketCount) 时返回 nullptr
 */
[[nodiscard]] const char* face_attribute_age_label(size_t bucket) noexcept;

/**
 * @brief 由年龄回归值（单位：年，非 0..100 视为非法）定位区间下标
 * @return 区间下标；年龄非法时返回 kFaceAttributeAgeBucketCount（调用方据此丢弃年龄属性）
 */
[[nodiscard]] size_t face_attribute_age_bucket(float age_years) noexcept;

/**
 * @brief 完整的人脸属性解释：性别 softmax + 年龄分档
 *
 * @param gender_logits 2 维性别 logits（0 = Female，1 = Male），**未做 softmax**
 * @param gender_count  必须等于 2
 * @param age_raw       年龄回归原始值（模型输出 ∈ [0, 1]，乘 100 为年）
 * @param age_count     必须等于 1
 * @return 过阈值的属性列表，按分数降序；性别与年龄段互斥各至多 1 条，
 *         因此最多 2 条。输入非法时返回空列表。
 */
[[nodiscard]] std::vector<zhulong::PersonAttribute> extract_face_attributes(
    const float* gender_logits, size_t gender_count, const float* age_raw, size_t age_count
);

/**
 * @brief 单张人脸的属性分类器
 *
 * 只负责「给定原帧与 5 点关键点，产出属性列表」；裁剪走 `Aligner` 的同一套相似变换，
 * 与特征提取的 112×112 warp 保持对齐语义一致（同一个对齐器、不同的输出尺寸）。
 */
class FaceAttributeClassifier {
public:
    /**
     * @brief 对单张人脸跑一次属性分类
     *
     * @param session    本 worker 的人脸属性会话
     * @param image      输入帧（只在本次调用期间有效）
     * @param landmarks_pixels 5 点**原帧像素**关键点（用 `pixel_landmarks()` 取，别在这里再乘宽高）
     * @param out_attributes   输出：过阈值的属性，按分数降序（失败时清空）
     * @return false 表示关键点退化、裁剪失败、模型输出形状/数值不合法
     */
    bool classify(zhulong::Session& session,
                  const zhulong::Image& image,
                  const std::array<Point2f, kLandmarkCount>& landmarks_pixels,
                  std::vector<zhulong::PersonAttribute>& out_attributes) const;
};

} // namespace zhulong::face

#endif // ZHULONG_FACE_RECOGNITION_FACE_ATTRIBUTE_HPP
