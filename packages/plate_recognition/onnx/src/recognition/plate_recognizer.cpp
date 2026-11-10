#include "plate_recognizer.hpp"

#include "common/plate_charset.hpp"

#include <zhulong/runtime/yuv_sampling.hpp>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <limits>
#include <utility>
#include <vector>

namespace zhulong::plate {

namespace {

/** @brief 识别模型的归一化参数（训练侧 mean/std） */
constexpr float kNormalizeMean = 0.588f;
constexpr float kNormalizeStd = 0.193f;
constexpr float kNormalizeScale = 1.0f / (255.0f * kNormalizeStd);
constexpr float kNormalizeShift = kNormalizeMean / kNormalizeStd;

/** @brief 类别数与形状都取不到时的回退值（该权重固定 21 时间步） */
constexpr std::size_t kFallbackTimesteps = 21;

/** @brief 打包像素的字节数；不支持的格式返回 0（由 frame_view_supported 统一拒绝） */
int packed_bytes_per_pixel(zhulong_pixel_format_t format) noexcept {
    switch (format) {
    case ZHULONG_PIXEL_FORMAT_RGB24:
    case ZHULONG_PIXEL_FORMAT_BGR24:
        return 3;
    default:
        return 0;
    }
}

/** @brief 双线性采样 packed 像素，按源内存顺序输出三个通道（不做通道翻转） */
inline void sample_packed_channels(
    const uint8_t* src, int w, int h, int stride,
    float fx, float fy, float& out_c0, float& out_c1, float& out_c2
) noexcept {
    fx = std::clamp(fx, 0.0f, static_cast<float>(w - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(h - 1));

    const int x0 = static_cast<int>(fx);
    const int y0 = static_cast<int>(fy);
    const int x1 = std::min(x0 + 1, w - 1);
    const int y1 = std::min(y0 + 1, h - 1);

    const float dx = fx - static_cast<float>(x0);
    const float dy = fy - static_cast<float>(y0);

    const auto row = [&](int y) noexcept {
        return src + static_cast<std::size_t>(y) * static_cast<std::size_t>(stride);
    };
    const uint8_t* p00 = row(y0) + static_cast<std::size_t>(x0) * 3;
    const uint8_t* p01 = row(y0) + static_cast<std::size_t>(x1) * 3;
    const uint8_t* p10 = row(y1) + static_cast<std::size_t>(x0) * 3;
    const uint8_t* p11 = row(y1) + static_cast<std::size_t>(x1) * 3;

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
 *
 * 前置条件：调用方已用 frame_view_supported() 校验过格式与平面完整性。
 */
inline void sample_pixel_bgr(
    const zhulong_frame_view& raw,
    float fx, float fy, float& out_b, float& out_g, float& out_r
) noexcept {
    const int w = raw.width;
    const int h = raw.height;
    const int uv_w = (w + 1) / 2;
    const int uv_h = (h + 1) / 2;
    const float uv_x = (fx + 0.5f) * static_cast<float>(uv_w) / static_cast<float>(w) - 0.5f;
    const float uv_y = (fy + 0.5f) * static_cast<float>(uv_h) / static_cast<float>(h) - 0.5f;

    const auto y_plane = [&]() noexcept {
        return zhulong::sample_plane_bilinear(raw.planes[0], w, h, raw.strides[0] > 0 ? raw.strides[0] : w, fx, fy);
    };

    if (raw.format == ZHULONG_PIXEL_FORMAT_YUV420P) {
        const int u_stride = raw.strides[1] > 0 ? raw.strides[1] : uv_w;
        const int v_stride = raw.strides[2] > 0 ? raw.strides[2] : uv_w;

        const float y_val = y_plane();
        const float u_val = zhulong::sample_plane_bilinear(raw.planes[1], uv_w, uv_h, u_stride, uv_x, uv_y);
        const float v_val = zhulong::sample_plane_bilinear(raw.planes[2], uv_w, uv_h, v_stride, uv_x, uv_y);
        zhulong::yuv_to_rgb(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    if (raw.format == ZHULONG_PIXEL_FORMAT_NV12) {
        const int uv_stride = raw.strides[1] > 0 ? raw.strides[1] : w;

        const float y_val = y_plane();
        float u_val = 128.0f;
        float v_val = 128.0f;
        zhulong::sample_nv12_uv_bilinear(raw.planes[1], uv_w, uv_h, uv_stride, uv_x, uv_y, u_val, v_val);
        zhulong::yuv_to_rgb(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    // 只剩 packed 3 通道（RGB24 / BGR24）：frame_view_supported 已把其它格式挡在外面。
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
        out_r = c0;
        out_g = c1;
        out_b = c2;
    }
}

/** @brief 识别输出 logits 的 [T, C] 维度解释 */
struct LogitsShape {
    std::size_t timesteps{0};
    std::size_t classes{0};
};

/**
 * @brief 从识别输出形状推断 [T, C] 布局
 *
 * 支持 [1, T, C] / [T, C]；形状缺失或退化时按元素总数与固定时间步回退。
 */
LogitsShape resolve_logits_shape(const zhulong::Tensor& tensor) {
    LogitsShape result;

    std::vector<std::size_t> dims;
    for (const int64_t dim : tensor.shape()) {
        if (dim == 1) continue; // 忽略 batch/squeeze 维
        if (dim <= 0) return {};
        dims.push_back(static_cast<std::size_t>(dim));
    }

    const std::size_t total = tensor.element_count();
    if (dims.size() >= 2) {
        result.timesteps = dims[dims.size() - 2];
        result.classes = dims.back();
    }
    if (result.timesteps == 0 || result.classes == 0) {
        // 回退：该权重固定 21 时间步；类别数由元素总数反推（不做任何类别数硬编码）
        if (total < kFallbackTimesteps) {
            return {};
        }
        result.timesteps = kFallbackTimesteps;
        result.classes = total / kFallbackTimesteps;
    }
    if (result.classes < 2 || result.timesteps == 0 || result.timesteps > total ||
        result.timesteps * result.classes > total) {
        return {};
    }
    return result;
}

/** @brief 单步 logits 的 argmax 结果 */
struct ArgmaxResult {
    std::size_t index{0};
    float max_value{0.0f};
};

/**
 * @brief logits 上的 argmax
 *
 * 非有限值（NaN / ±Inf）**不参与比较**：NaN 与任何数比较都为 false，若让它当了初值，
 * 整个序列的 argmax 会塌到索引 0 —— 那恰好是 CTC 空白符，于是「输出了一串垃圾」
 * 会表现成「这一步没字」，是最难查的一种静默。初值因此取 -inf 并显式跳过非有限值。
 *
 * 全部非有限时返回索引 0（空白符）与 max_value = 0：调用方据此把该步当作空白，
 * 且 softmax 分母不会被 NaN 污染。
 */
ArgmaxResult argmax_index(const float* logits, std::size_t count) noexcept {
    ArgmaxResult result{0, -std::numeric_limits<float>::infinity()};
    for (std::size_t i = 0; i < count; ++i) {
        const float value = logits[i];
        if (!std::isfinite(value)) {
            continue;
        }
        if (value > result.max_value) {
            result.max_value = value;
            result.index = i;
        }
    }
    if (!std::isfinite(result.max_value)) {
        return {};
    }
    return result;
}

/**
 * @brief 计算 argmax 类别的 softmax 概率
 *
 * 该模型导出**不含** Softmax（图末端是 Conv→Squeeze→Transpose），因此这里自行
 * 计算 `exp(x_max - max) / Σ exp(x - max)`；若将来换成带 Softmax 的导出，分数会
 * 整体变小，必须同步修改此处与参考实现。
 */
float softmax_probability(const float* logits, std::size_t count, float max_value) noexcept {
    float denominator = 0.0f;
    for (std::size_t i = 0; i < count; ++i) {
        if (!std::isfinite(logits[i])) {
            continue; // 单个 NaN 不能让整步的概率变成 NaN（否则分数静默变 0）
        }
        denominator += std::exp(logits[i] - max_value);
    }
    if (!(denominator > 0.0f) || !std::isfinite(denominator)) {
        return 0.0f;
    }
    return 1.0f / denominator;
}

} // namespace

bool frame_view_supported(const zhulong_frame_view& raw) noexcept {
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return false;
    }
    // 负 stride = 底朝上的缓冲区。静默当 top-down 读会产出上下翻转的图且没有错误码，
    // 是「图能出来、精度莫名下降」的典型来源；CPU 与 RGA 视觉后端同样拒绝。
    for (const int32_t stride : raw.strides) {
        if (stride < 0) {
            return false;
        }
    }

    // 正 stride 必须容得下一行像素（stride == 0 = 紧凑排列，由调用方按自然行宽推导）。
    // 不查这一条就会按声明的 stride 越界读 —— 与 SDK 视觉后端的入参校验口径保持一致。
    const int64_t width = static_cast<int64_t>(raw.width);
    const int64_t uv_w = (width + 1) / 2;
    const auto row_bytes_ok = [](int32_t stride, int64_t minimum) noexcept {
        return stride == 0 || static_cast<int64_t>(stride) >= minimum;
    };

    switch (raw.format) {
    case ZHULONG_PIXEL_FORMAT_RGB24:
    case ZHULONG_PIXEL_FORMAT_BGR24: {
        const int bytes_per_pixel = packed_bytes_per_pixel(raw.format);
        if (bytes_per_pixel == 0) {
            return false;
        }
        return row_bytes_ok(raw.strides[0], width * static_cast<int64_t>(bytes_per_pixel));
    }
    case ZHULONG_PIXEL_FORMAT_YUV420P:
        if (raw.planes[1] == nullptr || raw.planes[2] == nullptr) {
            return false;
        }
        return row_bytes_ok(raw.strides[0], width) && row_bytes_ok(raw.strides[1], uv_w) &&
               row_bytes_ok(raw.strides[2], uv_w);
    case ZHULONG_PIXEL_FORMAT_NV12:
        if (raw.planes[1] == nullptr) {
            return false;
        }
        // 交错 UV 平面一行是 2 * ceil(width/2) 字节（奇数宽下比 width 大 1）
        return row_bytes_ok(raw.strides[0], width) && row_bytes_ok(raw.strides[1], 2 * uv_w);
    default:
        // RGBA32 / UNKNOWN：检测侧的 letterbox 同样拒绝（SDK 视觉后端契约），
        // 两处必须给出一致结论，否则会出现「检出成功、识别恒空」的半死状态。
        return false;
    }
}

PlateSamplePoint plate_sample_point(int x, int y, int target_w, int target_h,
                                    float crop_x, float crop_y, float crop_w, float crop_h,
                                    PlateKind kind) noexcept {
    PlateSamplePoint point;

    if (kind == PlateKind::DoubleLine && target_w > 1 && target_h > 0) {
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
    PlateKind kind
) const {
    if (!session.valid() || !frame_view_supported(raw)) {
        return {};
    }

    const float raw_w = static_cast<float>(raw.width);
    const float raw_h = static_cast<float>(raw.height);
    const float box_w = (box[2] - box[0]) * raw_w;
    const float box_h = (box[3] - box[1]) * raw_h;
    constexpr float kHorizontalPadRatio = 0.045f;
    constexpr float kVerticalPadRatio = 0.04f;
    const float pad_x = box_w * kHorizontalPadRatio;
    const float pad_y = box_h * kVerticalPadRatio;

    const int x1 = std::clamp(static_cast<int>(std::floor(box[0] * raw_w - pad_x)), 0, raw.width - 1);
    const int y1 = std::clamp(static_cast<int>(std::floor(box[1] * raw_h - pad_y)), 0, raw.height - 1);
    const int x2 = std::clamp(static_cast<int>(std::ceil(box[2] * raw_w + pad_x)), x1 + 1, raw.width);
    const int y2 = std::clamp(static_cast<int>(std::ceil(box[3] * raw_h + pad_y)), y1 + 1, raw.height);
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

    constexpr std::size_t kPlaneSize = static_cast<std::size_t>(kRecognitionWidth) * kRecognitionHeight;
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
                static_cast<float>(crop_w), static_cast<float>(crop_h), kind);

            float b = 0.0f;
            float g = 0.0f;
            float r = 0.0f;
            sample_pixel_bgr(raw, point.fx, point.fy, b, g, r);

            const std::size_t index = static_cast<std::size_t>(y) * static_cast<std::size_t>(kRecognitionWidth) +
                                      static_cast<std::size_t>(x);
            plane_b[index] = b * kNormalizeScale - kNormalizeShift;
            plane_g[index] = g * kNormalizeScale - kNormalizeShift;
            plane_r[index] = r * kNormalizeScale - kNormalizeShift;
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

    const LogitsShape shape = resolve_logits_shape(outputs[0]);
    if (shape.timesteps == 0 || shape.classes < 2) {
        return {};
    }

    // 3. CTC 贪心解码：跳过空白符（索引 0）并合并连续重复字符
    RecognizedPlate result;
    result.kind = kind;

    std::size_t previous_index = 0;
    std::size_t recognized_chars = 0;
    float confidence_sum = 0.0f;
    std::string plate_number;
    plate_number.reserve(shape.timesteps);

    for (std::size_t t = 0; t < shape.timesteps; ++t) {
        const float* step_logits = char_logits + t * shape.classes;
        const ArgmaxResult best = argmax_index(step_logits, shape.classes);

        if (best.index != 0 && best.index != previous_index && best.index < kNumPlateChars) {
            plate_number += kPlateNames[best.index];
            confidence_sum += softmax_probability(step_logits, shape.classes, best.max_value);
            ++recognized_chars;
        }
        previous_index = best.index;
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
            const ArgmaxResult best_color = argmax_index(color_logits, kNumColors);
            result.plate_color = kColorNames[best_color.index];
        }
    }

    return result;
}

} // namespace zhulong::plate
