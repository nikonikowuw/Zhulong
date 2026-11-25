/**
 * @file face_attribute.cpp
 * @brief 人脸属性分类器实现（已对齐裁剪 → 后端输入布局 → 性别/年龄段解码）
 *
 * 模型：InsightFace `genderage`（性别 softmax 2 类 + 年龄回归 1 维）。
 * RK3568 档位用 INT8 量化的 `genderage_face.rknn`（450 KB）；onnx 档位目前未打包该模型，
 * 缺模型时静默降级，但源码已通过 `ZHULONG_INFERENCE_ONNX` 宏完整支持 NCHW FLOAT32 输入
 * 以及双输出 `[1,2]+[1,1]` / 官方单输出 `[1,3]`，补上 `genderage_face.onnx` 即可生效。
 * 本文件在两个档位逐字节相同。
 *
 * RKNN 转换时用 `SPLIT_OUTPUT=1` 把 `Concat(gender_logits[2], age[1]) -> fc1` 拆成两个
 * 输出，否则两个头共用同一个 INT8 scale，年龄量化步长会落到 5.2 年/档（实测 200 张
 * 相机人脸只剩 12 个不同年龄值），拆分后是 0.30 年/档。
 *
 * 预处理注意：ONNX 图入口自带归一化，转换时 `mean_values=[[0,0,0]] / std_values=[[1,1,1]]`、
 * `rgb2bgr=False`，所以 RKNN 直接喂 NHWC UINT8 原始 RGB，ONNX 转为 NCHW FLOAT32 原始
 * `[0, 255]` RGB（**不要**再做减均值/除方差，否则等于归一化两遍，输出整体失真却不报错）。
 */

#include "attribute/face_attribute.hpp"

#include "alignment/aligner.hpp"
#include "feature/extractor.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <exception>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief 输入视图是否可用（与检测器同一判据：平面指针、宽高与 stride 均合法） */
bool image_view_usable(const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    return raw.planes[0] != nullptr && raw.width > 0 && raw.height > 0 && raw.strides[0] >= 0;
}

/** @brief 把输入张量 move 进入参列表，避免 `forward({input})` 触发一次整块深拷贝 */
std::vector<zhulong::Tensor> single_input(zhulong::Tensor&& tensor) {
    std::vector<zhulong::Tensor> inputs;
    inputs.reserve(1);
    inputs.emplace_back(std::move(tensor));
    return inputs;
}

#if defined(ZHULONG_INFERENCE_ONNX)
/** @brief HWC uint8 RGB → NCHW float32（保留原始 [0, 255]，匹配模型图内归一化） */
void hwc_u8_to_nchw_f32(const uint8_t* src_hwc, int height, int width, float* dst_nchw) noexcept {
    const size_t plane = static_cast<size_t>(height) * static_cast<size_t>(width);
    float* dst_r = dst_nchw;
    float* dst_g = dst_nchw + plane;
    float* dst_b = dst_nchw + plane * 2;
    for (size_t i = 0; i < plane; ++i) {
        dst_r[i] = static_cast<float>(src_hwc[i * 3 + 0]);
        dst_g[i] = static_cast<float>(src_hwc[i * 3 + 1]);
        dst_b[i] = static_cast<float>(src_hwc[i * 3 + 2]);
    }
}
#endif

/** @brief 元素数查询；异常形状折为 0 */
size_t tensor_count_or_zero(const zhulong::Tensor& tensor) noexcept {
    try {
        return tensor.element_count();
    } catch (const std::exception&) {
        return 0;
    }
}

/** @brief 性别 logits 的 softmax 置信度（2 类，取较大者） */
float gender_confidence(const float* logits) {
    const float hi = std::max(logits[0], logits[1]);
    const float lo = std::min(logits[0], logits[1]);
    // softmax 平移不变：直接比 e^{hi-lo}，避免 exp 溢出
    return 1.0f / (1.0f + std::exp(lo - hi));
}

} // namespace

const char* face_attribute_age_label(size_t bucket) noexcept {
    static constexpr std::array<const char*, kFaceAttributeAgeBucketCount> kLabels{
        "Age0-18", "Age18-30", "Age30-45", "Age45-60", "Age60+",
    };
    if (bucket >= kLabels.size()) {
        return nullptr;
    }
    return kLabels[bucket];
}

size_t face_attribute_age_bucket(float age_years) noexcept {
    if (!std::isfinite(age_years) || age_years < 0.0f || age_years > kFaceAttributeAgeMaxYears) {
        return kFaceAttributeAgeBucketCount;
    }
    size_t bucket = 0;
    for (size_t i = 0; i < kFaceAttributeAgeBucketStarts.size(); ++i) {
        if (age_years >= kFaceAttributeAgeBucketStarts[i]) {
            bucket = i;
        }
    }
    return bucket;
}

std::vector<zhulong::PersonAttribute> extract_face_attributes(
    const float* gender_logits, size_t gender_count, const float* age_raw, size_t age_count
) {
    std::vector<zhulong::PersonAttribute> attributes;
    if (gender_logits == nullptr || gender_count != 2 || age_raw == nullptr || age_count != 1) {
        return attributes;
    }
    if (!std::isfinite(gender_logits[0]) || !std::isfinite(gender_logits[1]) || !std::isfinite(age_raw[0])) {
        return attributes;
    }

    // 类别顺序固定为 [female, male]（InsightFace genderage 的输出顺序）
    const bool is_male = gender_logits[1] > gender_logits[0];
    const float confidence = gender_confidence(gender_logits);
    if (confidence >= kFaceAttributeGenderThreshold) {
        attributes.push_back(zhulong::PersonAttribute{
            std::string(is_male ? kFaceAttributeMaleLabel : kFaceAttributeFemaleLabel), confidence});
    }

    // age_raw ∈ [0, 1] 是模型自己的刻度；越界说明输出被量化/后端污染，宁可不出年龄
    const float age_years = age_raw[0] * 100.0f;
    const size_t bucket = face_attribute_age_bucket(age_years);
    if (const char* label = face_attribute_age_label(bucket); label != nullptr) {
        // 年龄回归没有可用的概率语义：用 softmax 的性别置信度冒充年龄置信度是编造的。
        // 固定给 1.0，语义是「该年龄段被选中」，权重判断始终看性别那一项。
        attributes.push_back(zhulong::PersonAttribute{std::string(label), kFaceAttributeAgeSelectedScore});
    }

    std::stable_sort(attributes.begin(), attributes.end(),
                     [](const zhulong::PersonAttribute& lhs, const zhulong::PersonAttribute& rhs) {
                         return lhs.score > rhs.score;
                     });
    return attributes;
}

std::vector<zhulong::PersonAttribute> extract_face_attributes_from_tensors(
    const std::vector<zhulong::Tensor>& outputs
) {
    if (outputs.size() == 2 &&
        outputs[0].dtype() == zhulong::DataType::FLOAT32 &&
        tensor_count_or_zero(outputs[0]) == kFaceAttributeGenderCount &&
        outputs[1].dtype() == zhulong::DataType::FLOAT32 &&
        tensor_count_or_zero(outputs[1]) == kFaceAttributeAgeCount) {
        return extract_face_attributes(outputs[0].data<float>(), kFaceAttributeGenderCount,
                                       outputs[1].data<float>(), kFaceAttributeAgeCount);
    }
#if defined(ZHULONG_INFERENCE_ONNX)
    constexpr size_t kCombinedCount = kFaceAttributeGenderCount + kFaceAttributeAgeCount;
    if (outputs.size() == 1 &&
        outputs[0].dtype() == zhulong::DataType::FLOAT32 &&
        tensor_count_or_zero(outputs[0]) == kCombinedCount) {
        const float* combined = outputs[0].data<float>();
        if (combined != nullptr) {
            return extract_face_attributes(combined, kFaceAttributeGenderCount,
                                           combined + kFaceAttributeGenderCount, kFaceAttributeAgeCount);
        }
    }
#endif
    return {};
}

bool FaceAttributeClassifier::classify(
    zhulong::Session& session,
    const zhulong::Image& image,
    const std::array<Point2f, kLandmarkCount>& landmarks_pixels,
    std::vector<zhulong::PersonAttribute>& out_attributes
) const {
    out_attributes.clear();
    if (!session.valid() || !image_view_usable(image)) {
        return false;
    }

    // 与特征提取同一套 5 点相似变换：先把关键点映射进 112×112 模板，再把模板按比例
    // 缩到 96×96。两者共用同一个对齐语义，不会出现「特征用一套模板、属性用另一套」。
    std::array<float, 6> rec_affine{};
    if (!Aligner::estimate_similarity_transform(landmarks_pixels, rec_affine)) {
        return false;
    }
    const float scale = static_cast<float>(kFaceAttributeInputWidth) / static_cast<float>(kFeaturePatchWidth);
    const std::array<float, 6> affine{
        rec_affine[0] * scale, rec_affine[1] * scale, rec_affine[2] * scale,
        rec_affine[3] * scale, rec_affine[4] * scale, rec_affine[5] * scale,
    };

    zhulong::Tensor patch({1, kFaceAttributeInputHeight, kFaceAttributeInputWidth, 3},
                          zhulong::DataType::UINT8);
    if (!Aligner::crop_and_align_direct(image, affine, patch.data<uint8_t>(), patch.element_count(),
                                        kFaceAttributeInputWidth, kFaceAttributeInputHeight)) {
        return false;
    }

#if defined(ZHULONG_INFERENCE_ONNX)
    zhulong::Tensor onnx_input({1, 3, kFaceAttributeInputHeight, kFaceAttributeInputWidth},
                               zhulong::DataType::FLOAT32);
    hwc_u8_to_nchw_f32(patch.data<uint8_t>(), kFaceAttributeInputHeight, kFaceAttributeInputWidth,
                       onnx_input.data<float>());
    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(onnx_input)));
#else
    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(patch)));
#endif

    out_attributes = extract_face_attributes_from_tensors(outputs);
    return !out_attributes.empty();
}

} // namespace zhulong::face
