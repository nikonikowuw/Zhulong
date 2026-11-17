/**
 * @file person_attribute.cpp
 * @brief 人体属性分类器的 ONNX 档位实现（HWC uint8 裁剪 → NCHW float32 逐通道归一化）
 *
 * 两档位的差异**只有前处理**：`PPLCNet_x1_0_person_attribute.onnx` 是原始权重，
 * 归一化必须在这里做；rk3568 的 `.rknn` 在转换时把 mean/std 烘进了模型，直接喂
 * HWC uint8 即可（见 rk3568 目录下同名文件）。
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

/** @brief 属性模型逐通道归一化参数（与 model zoo convert.py 的 mean/std 一致，0~255 量纲） */
constexpr std::array<float, 3> kAttributeMean{123.675f, 116.28f, 103.53f};
constexpr std::array<float, 3> kAttributeStd{58.395f, 57.12f, 57.375f};

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

/**
 * @brief HWC(uint8 RGB) → NCHW(float32)，逐通道 `(x - mean_c) / std_c`
 *
 * 只做平面拆分与线性变换：没有做任何 letterbox/裁边，几何变换已在 `Aligner` 里完成。
 * 单帧 147456 像素，逐通道一趟顺序读写即可，不需要额外临时缓冲。
 */
void hwc_to_nchw_normalized(const uint8_t* src_hwc, int height, int width, float* dst_nchw) {
    const size_t plane = static_cast<size_t>(height) * static_cast<size_t>(width);
    for (size_t channel = 0; channel < 3; ++channel) {
        const float mean = kAttributeMean[channel];
        const float inv_std = 1.0f / kAttributeStd[channel];
        float* dst = dst_nchw + plane * channel;
        const uint8_t* src = src_hwc + channel;
        for (size_t i = 0; i < plane; ++i) {
            dst[i] = (static_cast<float>(src[i * 3]) - mean) * inv_std;
        }
    }
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

    // 归一化框 → 像素框，并夹回画面内：人体框由本包的检测器给出，理论上已在 [0,1]，
    // 这里再夹一次是为了让「框贴边」的裁剪不会因为越界坐标产生负 stride。
    const float x1 = std::clamp(body_box[0], 0.0f, 1.0f) * static_cast<float>(image_w);
    const float y1 = std::clamp(body_box[1], 0.0f, 1.0f) * static_cast<float>(image_h);
    const float x2 = std::clamp(body_box[2], 0.0f, 1.0f) * static_cast<float>(image_w);
    const float y2 = std::clamp(body_box[3], 0.0f, 1.0f) * static_cast<float>(image_h);
    if (x2 - x1 < 2.0f || y2 - y1 < 2.0f) {
        return false;
    }

    // 1. 直接裁剪到模型输入尺寸：属性模型按「检测框整块拉伸」训练（不做 letterbox、不保持长宽比），
    //    这里必须保持一致；用 2×3 仿射矩阵表达纯缩放 + 平移，交给 Aligner 做双线性采样。
    zhulong::Tensor patch({kPersonAttributeInputHeight, kPersonAttributeInputWidth, 3}, zhulong::DataType::UINT8);
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

    // 2. 原始 ONNX 权重不含归一化，必须在 Host 侧补齐（rk3568 档位由模型自带，不重复做）
    zhulong::Tensor input({1, 3, kPersonAttributeInputHeight, kPersonAttributeInputWidth},
                          zhulong::DataType::FLOAT32);
    hwc_to_nchw_normalized(patch.data<uint8_t>(), kPersonAttributeInputHeight, kPersonAttributeInputWidth,
                           input.data<float>());

    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(input)));
    if (outputs.size() != 1 || outputs[0].dtype() != zhulong::DataType::FLOAT32 ||
        outputs[0].element_count() != static_cast<size_t>(kPersonAttributeCount)) {
        return false;
    }

    out_attributes = extract_person_attributes(outputs[0].data<float>(), outputs[0].element_count());
    return true;
}

} // namespace zhulong::face
