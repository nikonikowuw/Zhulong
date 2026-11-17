/**
 * @file person_attribute.cpp
 * @brief 人体属性分类器的 rk3568 档位实现（HWC uint8 直接送入 NPU）
 *
 * `.rknn` 在转换时把 mean/std 烘进了模型（convert.py 的 `mean_values`/`std_values`），
 * 运行时只需要给拉伸后的 RGB888 块 —— **不要**在这里再做一次归一化，否则等于对
 * 归一化后的值再减一次均值，输出会整体失真但不会报错。
 */

#include "attribute/person_attribute.hpp"

#include "alignment/aligner.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
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

} // namespace

bool PersonAttributeClassifier::classify(zhulong::Session& session,
                                         const zhulong::Image& image,
                                         const std::array<float, 4>& body_box,
                                         std::vector<zhulong::PersonAttribute>& out_attributes) const {
    out_attributes.clear();
    if (!session.valid() || !image_view_usable(image)) {
        return false;
    }

    const auto& raw = image.raw_view();
    const int image_w = raw.width;
    const int image_h = raw.height;
    if (!(body_box[2] > body_box[0]) || !(body_box[3] > body_box[1])) {
        return false;
    }

    const float x1 = std::clamp(body_box[0], 0.0f, 1.0f) * static_cast<float>(image_w);
    const float y1 = std::clamp(body_box[1], 0.0f, 1.0f) * static_cast<float>(image_h);
    const float x2 = std::clamp(body_box[2], 0.0f, 1.0f) * static_cast<float>(image_w);
    const float y2 = std::clamp(body_box[3], 0.0f, 1.0f) * static_cast<float>(image_h);
    if (x2 - x1 < 2.0f || y2 - y1 < 2.0f) {
        return false;
    }

    // 属性模型按「检测框整块拉伸到 192×256」训练，不 letterbox、不保持长宽比。
    // NHWC 四维张量与模型输入元素数一致（1×256×192×3），NPU 侧按模型自身的
    // NHWC uint8 输入约定取用，mean/std 已在模型内完成。
    zhulong::Tensor patch({1, kPersonAttributeInputHeight, kPersonAttributeInputWidth, 3},
                          zhulong::DataType::UINT8);
    const float scale_x = static_cast<float>(kPersonAttributeInputWidth) / (x2 - x1);
    const float scale_y = static_cast<float>(kPersonAttributeInputHeight) / (y2 - y1);
    const std::array<float, 6> affine{
        scale_x, 0.0f, -x1 * scale_x,
        0.0f, scale_y, -y1 * scale_y,
    };
    if (!Aligner::crop_and_align_direct(image, affine, patch.data<uint8_t>(), patch.element_count(),
                                        kPersonAttributeInputWidth, kPersonAttributeInputHeight)) {
        return false;
    }

    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(patch)));
    if (outputs.size() != 1 || outputs[0].dtype() != zhulong::DataType::FLOAT32 ||
        outputs[0].element_count() != static_cast<size_t>(kPersonAttributeCount)) {
        return false;
    }

    out_attributes = extract_person_attributes(outputs[0].data<float>(), outputs[0].element_count());
    return true;
}

} // namespace zhulong::face
