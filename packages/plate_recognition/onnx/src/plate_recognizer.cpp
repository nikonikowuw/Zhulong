#include "plate_recognizer.hpp"

#include <zhulong/runtime/yuv_sampling.hpp>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <vector>

namespace zhulong::plate {

namespace {

/**
 * @brief 车牌字符表（CTC 类别索引，0 为空白符）
 *
 * ⚠️ 顺序即模型类别顺序，改动会导致全线串码。必须与训练侧
 * （以及参考实现 scripts/test_infer.py 的 PLATE_NAMES）逐字节一致。
 */
const char* const kPlateNames[] = {
    "#", "京", "沪", "津", "渝", "冀", "晋", "蒙", "辽", "吉", "黑",
    "苏", "浙", "皖", "闽", "赣", "鲁", "豫", "鄂", "湘", "粤",
    "桂", "琼", "川", "贵", "云", "藏", "陕", "甘", "青", "宁",
    "新", "学", "警", "港", "澳", "挂", "使", "领", "民", "航", "危",
    "0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
    "A", "B", "C", "D", "E", "F", "G", "H", "J", "K",
    "L", "M", "N", "P", "Q", "R", "S", "T", "U", "V",
    "W", "X", "Y", "Z", "险", "品"
};
constexpr size_t kNumPlateChars = sizeof(kPlateNames) / sizeof(kPlateNames[0]);

/** @brief 颜色分类类别表（顺序即模型类别顺序） */
const char* const kColorNames[] = {"黑色", "蓝色", "绿色", "白色", "黄色"};
constexpr size_t kNumColors = sizeof(kColorNames) / sizeof(kColorNames[0]);

/** @brief 识别模型的归一化参数（训练侧 mean/std） */
constexpr float kNormalizeMean = 0.588f;
constexpr float kNormalizeStd = 0.193f;

/** @brief 类别数与形状都取不到时的回退值（该权重固定 21 时间步） */
constexpr size_t kFallbackTimesteps = 21;

/** @brief 双线性采样 packed 像素，按源内存顺序输出三个通道（不做通道翻转） */
inline void sample_packed_channels(
    const uint8_t* src, int w, int h, int stride,
    float fx, float fy, float& out_c0, float& out_c1, float& out_c2
) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(w - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(h - 1));

    const int x0 = static_cast<int>(fx);
    const int y0 = static_cast<int>(fy);
    const int x1 = std::min(x0 + 1, w - 1);
    const int y1 = std::min(y0 + 1, h - 1);

    const float dx = fx - x0;
    const float dy = fy - y0;

    const uint8_t* p00 = src + static_cast<size_t>(y0) * stride + static_cast<size_t>(x0) * 3;
    const uint8_t* p01 = src + static_cast<size_t>(y0) * stride + static_cast<size_t>(x1) * 3;
    const uint8_t* p10 = src + static_cast<size_t>(y1) * stride + static_cast<size_t>(x0) * 3;
    const uint8_t* p11 = src + static_cast<size_t>(y1) * stride + static_cast<size_t>(x1) * 3;

    out_c0 = (1.0f - dx) * (1.0f - dy) * p00[0] + dx * (1.0f - dy) * p01[0] +
             (1.0f - dx) * dy * p10[0] + dx * dy * p11[0];
    out_c1 = (1.0f - dx) * (1.0f - dy) * p00[1] + dx * (1.0f - dy) * p01[1] +
             (1.0f - dx) * dy * p10[1] + dx * dy * p11[1];
    out_c2 = (1.0f - dx) * (1.0f - dy) * p00[2] + dx * (1.0f - dy) * p01[2] +
             (1.0f - dx) * dy * p10[2] + dx * dy * p11[2];
}

/**
 * @brief 按像素格式采样单个像素，输出 **BGR** 顺序的浮点值
 *
 * 通道顺序由识别模型的训练侧决定：参考实现 scripts/test_infer.py 直接把 cv2 的
 * BGR 数组喂给识别模型（检测模型才做了 BGR→RGB 翻转）。喂 RGB 会让颜色头系统性
 * 判错（实测蓝牌被读成黑牌），且字符也会漂，因此这里显式输出 BGR。
 *
 * 采样数学与 CPU 视觉后端共用 yuv_sampling.hpp：喂模型的前处理与裁给识别器的
 * 局部采样必须颜色一致，否则同一路视频在两条路径上会得到不同颜色。
 */
inline void sample_pixel_bgr(
    const zhulong_frame_view& raw,
    float fx, float fy, float& out_b, float& out_g, float& out_r
) {
    const int w = raw.width;
    const int h = raw.height;
    const int uv_w = (w + 1) / 2;
    const int uv_h = (h + 1) / 2;
    const float uv_x = (fx + 0.5f) * static_cast<float>(uv_w) / static_cast<float>(w) - 0.5f;
    const float uv_y = (fy + 0.5f) * static_cast<float>(uv_h) / static_cast<float>(h) - 0.5f;

    if (raw.format == ZHULONG_PIXEL_FORMAT_YUV420P && raw.planes[0] && raw.planes[1] && raw.planes[2]) {
        const int u_stride = raw.strides[1] > 0 ? raw.strides[1] : uv_w;
        const int v_stride = raw.strides[2] > 0 ? raw.strides[2] : uv_w;

        const float y_val = zhulong::sample_plane_bilinear(raw.planes[0], w, h, raw.strides[0] > 0 ? raw.strides[0] : w, fx, fy);
        const float u_val = zhulong::sample_plane_bilinear(raw.planes[1], uv_w, uv_h, u_stride, uv_x, uv_y);
        const float v_val = zhulong::sample_plane_bilinear(raw.planes[2], uv_w, uv_h, v_stride, uv_x, uv_y);
        zhulong::yuv_to_rgb(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    if (raw.format == ZHULONG_PIXEL_FORMAT_NV12 && raw.planes[0] && raw.planes[1]) {
        const int uv_stride = raw.strides[1] > 0 ? raw.strides[1] : w;

        const float y_val = zhulong::sample_plane_bilinear(raw.planes[0], w, h, raw.strides[0] > 0 ? raw.strides[0] : w, fx, fy);
        float u_val = 128.0f;
        float v_val = 128.0f;
        zhulong::sample_nv12_uv_bilinear(raw.planes[1], uv_w, uv_h, uv_stride, uv_x, uv_y, u_val, v_val);
        zhulong::yuv_to_rgb(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    if (raw.planes[0]) {
        const int stride = raw.strides[0] > 0 ? raw.strides[0] : w * 3;
        float c0 = 0.0f;
        float c1 = 0.0f;
        float c2 = 0.0f;
        sample_packed_channels(raw.planes[0], w, h, stride, fx, fy, c0, c1, c2);
        if (raw.format == ZHULONG_PIXEL_FORMAT_BGR24) {
            out_b = c0;
            out_g = c1;
            out_r = c2;
        } else {
            // RGB24 及未知打包格式按 RGB 解释（与 CPU 视觉后端的默认假设一致）
            out_r = c0;
            out_g = c1;
            out_b = c2;
        }
        return;
    }

    out_b = 0.0f;
    out_g = 0.0f;
    out_r = 0.0f;
}

/**
 * @brief 从识别输出形状推断 [T, C] 布局
 *
 * 支持 [1, T, C] / [T, C]；形状缺失或退化时按元素总数与固定时间步回退。
 */
void resolve_logits_shape(const zhulong::Tensor& tensor, size_t& timesteps, size_t& classes) {
    timesteps = 0;
    classes = 0;

    std::vector<size_t> dims;
    for (const int64_t dim : tensor.shape()) {
        if (dim == 1) continue; // 忽略 batch/squeeze 维
        if (dim <= 0) return;
        dims.push_back(static_cast<size_t>(dim));
    }

    const size_t total = tensor.element_count();
    if (dims.size() >= 2) {
        timesteps = dims[dims.size() - 2];
        classes = dims.back();
    }
    if (timesteps == 0 || classes == 0) {
        // 回退：该权重固定 21 时间步；类别数由元素总数反推（不做任何类别数硬编码）
        timesteps = kFallbackTimesteps;
        if (total < timesteps) {
            timesteps = 0;
            return;
        }
        classes = total / timesteps;
    }
    if (classes < 2 || timesteps == 0 || timesteps > total || timesteps * classes > total) {
        timesteps = 0;
        classes = 0;
    }
}

/** @brief logits 上的 argmax；NaN 不参与比较，全 NaN 时返回索引 0 */
void argmax_index(const float* logits, size_t count, size_t& best_index, float& max_value) {
    best_index = 0;
    max_value = logits[0];
    for (size_t i = 1; i < count; ++i) {
        if (logits[i] > max_value) {
            max_value = logits[i];
            best_index = i;
        }
    }
}

/**
 * @brief 计算 argmax 类别的 softmax 概率
 *
 * 该模型导出**不含** Softmax（图末端是 Conv→Squeeze→Transpose），因此这里自行
 * 计算 `exp(x_max - max) / Σ exp(x - max)`；若将来换成带 Softmax 的导出，分数会
 * 整体变小，必须同步修改此处与参考实现。
 */
float softmax_probability(const float* logits, size_t count, float max_value) {
    float denominator = 0.0f;
    for (size_t i = 0; i < count; ++i) {
        denominator += std::exp(logits[i] - max_value);
    }
    if (!(denominator > 0.0f) || !std::isfinite(denominator)) {
        return 0.0f;
    }
    return 1.0f / denominator;
}

} // namespace

PlateSamplePoint plate_sample_point(int x, int y, int target_w, int target_h,
                                    float crop_x, float crop_y, float crop_w, float crop_h,
                                    int32_t plate_type) {
    PlateSamplePoint point;

    if (plate_type == static_cast<int32_t>(PlateLineLayout::DoubleLine) && target_w > 1 && target_h > 0) {
        // 双层车牌：上半取 5/12 高、下半取 2/3 高，各自铺满一行后左右拼接
        const int half_width = target_w / 2;
        const float upper_height = crop_h * 5.0f / 12.0f;
        const float lower_start = crop_h / 3.0f;
        const float lower_height = crop_h - lower_start;

        if (x < half_width) {
            const float sub_w = static_cast<float>(half_width);
            point.fx = crop_x + (static_cast<float>(x) + 0.5f) * (crop_w / sub_w) - 0.5f;
            point.fy = crop_y + (static_cast<float>(y) + 0.5f) * (upper_height / static_cast<float>(target_h)) - 0.5f;
        } else {
            const int local_x = x - half_width;
            const float sub_w = static_cast<float>(target_w - half_width);
            point.fx = crop_x + (static_cast<float>(local_x) + 0.5f) * (crop_w / sub_w) - 0.5f;
            point.fy = crop_y + lower_start + (static_cast<float>(y) + 0.5f) * (lower_height / static_cast<float>(target_h)) - 0.5f;
        }
        return point;
    }

    point.fx = crop_x + (static_cast<float>(x) + 0.5f) * (crop_w / static_cast<float>(target_w)) - 0.5f;
    point.fy = crop_y + (static_cast<float>(y) + 0.5f) * (crop_h / static_cast<float>(target_h)) - 0.5f;
    return point;
}

RecognizedPlate PlateRecognizer::recognize(
    zhulong::Session& session,
    const zhulong_frame_view& raw,
    const std::array<float, 4>& box,
    int32_t class_id
) {
    if (!session.valid() || raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    const int x1 = std::clamp(static_cast<int>(box[0] * static_cast<float>(raw.width)), 0, raw.width - 1);
    const int y1 = std::clamp(static_cast<int>(box[1] * static_cast<float>(raw.height)), 0, raw.height - 1);
    const int x2 = std::clamp(static_cast<int>(box[2] * static_cast<float>(raw.width)), x1 + 1, raw.width);
    const int y2 = std::clamp(static_cast<int>(box[3] * static_cast<float>(raw.height)), y1 + 1, raw.height);
    const int crop_w = x2 - x1;
    const int crop_h = y2 - y1;
    if (crop_w <= 0 || crop_h <= 0) {
        return {};
    }

    // 1. 前处理：按层数语义采样到 168×48，通道顺序 BGR（见 sample_pixel_bgr）
    zhulong::Tensor input_tensor({1, 3, kRecognitionHeight, kRecognitionWidth}, zhulong::DataType::FLOAT32);
    float* planes = input_tensor.data<float>();
    if (planes == nullptr) {
        return {};
    }

    constexpr size_t kPlaneSize = static_cast<size_t>(kRecognitionWidth) * kRecognitionHeight;
    // ⚠️ BGR：模型训练侧吃 cv2 的 BGR（test_infer.py 未做通道翻转），喂 RGB 会让
    //    颜色头系统性误判。三行注释对应 NCHW 的 channel 0/1/2。
    float* plane_b = planes;
    float* plane_g = planes + kPlaneSize;
    float* plane_r = planes + kPlaneSize * 2;

    for (int y = 0; y < kRecognitionHeight; ++y) {
        for (int x = 0; x < kRecognitionWidth; ++x) {
            const PlateSamplePoint point = plate_sample_point(
                x, y, kRecognitionWidth, kRecognitionHeight,
                static_cast<float>(x1), static_cast<float>(y1),
                static_cast<float>(crop_w), static_cast<float>(crop_h), class_id);

            float b = 0.0f;
            float g = 0.0f;
            float r = 0.0f;
            sample_pixel_bgr(raw, point.fx, point.fy, b, g, r);

            const size_t index = static_cast<size_t>(y) * kRecognitionWidth + x;
            plane_b[index] = (b / 255.0f - kNormalizeMean) / kNormalizeStd;
            plane_g[index] = (g / 255.0f - kNormalizeMean) / kNormalizeStd;
            plane_r[index] = (r / 255.0f - kNormalizeMean) / kNormalizeStd;
        }
    }

    // 2. 前向推理
    auto outputs = session.forward({input_tensor});
    if (outputs.empty() || outputs[0].element_count() < kNumPlateChars) {
        return {};
    }
    const float* char_logits = outputs[0].data<float>();
    if (char_logits == nullptr) {
        return {};
    }

    size_t timesteps = 0;
    size_t num_classes = 0;
    resolve_logits_shape(outputs[0], timesteps, num_classes);
    if (timesteps == 0 || num_classes < 2) {
        return {};
    }

    // 3. CTC 贪心解码：跳过空白符（索引 0）并合并连续重复字符
    RecognizedPlate result;
    result.plate_type = class_id;

    size_t previous_index = 0;
    size_t recognized_chars = 0;
    float confidence_sum = 0.0f;
    std::string plate_number;
    plate_number.reserve(timesteps);

    for (size_t t = 0; t < timesteps; ++t) {
        const float* step_logits = char_logits + t * num_classes;
        size_t best_index = 0;
        float max_value = 0.0f;
        argmax_index(step_logits, num_classes, best_index, max_value);

        if (best_index != 0 && best_index != previous_index && best_index < kNumPlateChars) {
            plate_number += kPlateNames[best_index];
            confidence_sum += softmax_probability(step_logits, num_classes, max_value);
            ++recognized_chars;
        }
        previous_index = best_index;
    }

    if (plate_number.empty() || recognized_chars == 0) {
        return {};
    }
    result.plate_number = std::move(plate_number);
    result.score = confidence_sum / static_cast<float>(recognized_chars);

    // 4. 颜色分类（同一模型第二路输出，末端已 ReduceMean，无需 Softmax）
    if (outputs.size() > 1 && outputs[1].element_count() >= kNumColors) {
        const float* color_logits = outputs[1].data<float>();
        if (color_logits != nullptr) {
            size_t best_color = 0;
            float max_value = 0.0f;
            argmax_index(color_logits, kNumColors, best_color, max_value);
            result.plate_color = kColorNames[best_color];
        }
    }

    return result;
}

} // namespace zhulong::plate
