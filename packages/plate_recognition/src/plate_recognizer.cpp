#include "plate_recognizer.hpp"
#include <algorithm/runtime/yuv_sampling.hpp>
#include <algorithm>
#include <array>
#include <cmath>

namespace zhulong::plate {

namespace {

static const char* PLATE_NAMES[] = {
    "#", "京", "沪", "津", "渝", "冀", "晋", "蒙", "辽", "吉", "黑",
    "苏", "浙", "皖", "闽", "赣", "鲁", "豫", "鄂", "湘", "粤",
    "桂", "琼", "川", "贵", "云", "藏", "陕", "甘", "青", "宁",
    "新", "学", "警", "港", "澳", "挂", "使", "领", "民", "航", "危",
    "0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
    "A", "B", "C", "D", "E", "F", "G", "H", "J", "K",
    "L", "M", "N", "P", "Q", "R", "S", "T", "U", "V",
    "W", "X", "Y", "Z", "险", "品"
};
constexpr size_t NUM_PLATE_CHARS = sizeof(PLATE_NAMES) / sizeof(PLATE_NAMES[0]);

static const char* COLOR_NAMES[] = {"黑色", "蓝色", "绿色", "白色", "黄色"};
constexpr size_t NUM_COLORS = sizeof(COLOR_NAMES) / sizeof(COLOR_NAMES[0]);

// 双线性插值采样像素，统一输出 RGB 浮点通道（由 sample_packed_rgb 的 BGR 顺序纠正）
inline void sample_packed_rgb(
    const uint8_t* src, int w, int h, int stride,
    float fx, float fy, float& out_b, float& out_g, float& out_r
) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(w - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(h - 1));

    int x0 = static_cast<int>(fx);
    int y0 = static_cast<int>(fy);
    int x1 = std::min(x0 + 1, w - 1);
    int y1 = std::min(y0 + 1, h - 1);

    float dx = fx - x0;
    float dy = fy - y0;

    const uint8_t* p00 = src + static_cast<size_t>(y0) * stride + x0 * 3;
    const uint8_t* p01 = src + static_cast<size_t>(y0) * stride + x1 * 3;
    const uint8_t* p10 = src + static_cast<size_t>(y1) * stride + x0 * 3;
    const uint8_t* p11 = src + static_cast<size_t>(y1) * stride + x1 * 3;

    for (int c = 0; c < 3; ++c) {
        float val = (1.0f - dx) * (1.0f - dy) * p00[c] +
                    dx * (1.0f - dy) * p01[c] +
                    (1.0f - dx) * dy * p10[c] +
                    dx * dy * p11[c];
        if (c == 0) out_b = val;
        else if (c == 1) out_g = val;
        else out_r = val;
    }
}

/**
 * @brief 按像素格式采样单个像素，输出归一化前的 RGB 浮点值
 *
 * 支持 YUV420P / NV12 / packed RGB24・BGR24。采样数学与 CPU 视觉后端共用
 * yuv_sampling.hpp：两条路径（喂模型的前处理 vs 裁给识别器的局部采样）必须颜色一致。
 */
inline void sample_pixel_rgb(
    const zhulong_frame_view& raw,
    float fx, float fy, float& out_r, float& out_g, float& out_b
) {
    const int w = raw.width;
    const int h = raw.height;
    const int uv_w = (w + 1) / 2;
    const int uv_h = (h + 1) / 2;
    const float uv_x = (fx + 0.5f) * uv_w / w - 0.5f;
    const float uv_y = (fy + 0.5f) * uv_h / h - 0.5f;

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
        sample_packed_rgb(raw.planes[0], w, h, stride, fx, fy, c0, c1, c2);
        if (raw.format == ZHULONG_PIXEL_FORMAT_BGR24) {
            out_r = c2;
            out_g = c1;
            out_b = c0;
        } else {
            out_r = c0;
            out_g = c1;
            out_b = c2;
        }
        return;
    }

    out_r = 0.0f;
    out_g = 0.0f;
    out_b = 0.0f;
}

} // namespace

RecognizedPlate PlateRecognizer::recognize(
    zhulong::Session& session,
    const zhulong_frame_view& raw,
    const std::array<float, 4>& box,
    int32_t class_id
) {
    if (!session.valid() || raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    int x1 = std::clamp(static_cast<int>(box[0] * raw.width), 0, raw.width - 1);
    int y1 = std::clamp(static_cast<int>(box[1] * raw.height), 0, raw.height - 1);
    int x2 = std::clamp(static_cast<int>(box[2] * raw.width), x1 + 1, raw.width);
    int y2 = std::clamp(static_cast<int>(box[3] * raw.height), y1 + 1, raw.height);
    int crop_w = x2 - x1;
    int crop_h = y2 - y1;

    if (crop_w <= 0 || crop_h <= 0) {
        return {};
    }

    constexpr int target_w = 168;
    constexpr int target_h = 48;

    zhulong::Tensor input_tensor({1, 3, target_h, target_w}, zhulong::DataType::FLOAT32);
    float* r_plane = input_tensor.data<float>();
    float* g_plane = r_plane + target_h * target_w;
    float* b_plane = g_plane + target_h * target_w;

    constexpr float mean_val = 0.588f;
    constexpr float std_val = 0.193f;

    for (int y = 0; y < target_h; ++y) {
        for (int x = 0; x < target_w; ++x) {
            float fx = 0.0f;
            float fy = 0.0f;

            if (class_id == 1) {
                // 双层车牌切块重拼：左半边采样上半部分，右半边采样下半部分
                if (x < target_w / 2) {
                    float sub_w = static_cast<float>(target_w / 2);
                    fx = x1 + (x + 0.5f) * (crop_w / sub_w) - 0.5f;
                    fy = y1 + (y + 0.5f) * ((crop_h * 5.0f / 12.0f) / target_h) - 0.5f;
                } else {
                    float sub_w = static_cast<float>(target_w / 2);
                    fx = x1 + (x - target_w / 2 + 0.5f) * (crop_w / sub_w) - 0.5f;
                    float lower_start = crop_h * (1.0f / 3.0f);
                    fy = y1 + lower_start + (y + 0.5f) * ((crop_h - lower_start) / target_h) - 0.5f;
                }
            } else {
                // 单层车牌常规采样
                fx = x1 + (x + 0.5f) * (crop_w / static_cast<float>(target_w)) - 0.5f;
                fy = y1 + (y + 0.5f) * (crop_h / static_cast<float>(target_h)) - 0.5f;
            }

            float r = 0.0f, g = 0.0f, b = 0.0f;
            sample_pixel_rgb(raw, fx, fy, r, g, b);

            int idx = y * target_w + x;
            r_plane[idx] = (r / 255.0f - mean_val) / std_val;
            g_plane[idx] = (g / 255.0f - mean_val) / std_val;
            b_plane[idx] = (b / 255.0f - mean_val) / std_val;
        }
    }

    auto outputs = session.forward({input_tensor});
    if (outputs.empty() || outputs[0].element_count() < NUM_PLATE_CHARS) {
        return {};
    }

    RecognizedPlate res;
    res.plate_type = class_id;

    // 1. 解析车牌字符序列 (CTC Greedy Decode)
    const float* char_logits = outputs[0].data<float>();
    if (!char_logits) {
        return {};
    }
    size_t timesteps = 21;
    size_t num_classes = NUM_PLATE_CHARS;

    const auto& out0_shape = outputs[0].shape();
    if (out0_shape.size() >= 3) {
        timesteps = out0_shape[1];
        num_classes = out0_shape[2];
    }

    if (timesteps == 0 || num_classes == 0 || timesteps * num_classes > outputs[0].element_count()) {
        return {};
    }

    size_t pre_idx = 0;
    size_t recognized_chars = 0;
    float confidence_sum = 0.0f;
    std::string plate_no;

    for (size_t t = 0; t < timesteps; ++t) {
        const float* step_logits = char_logits + t * num_classes;
        size_t best_c = 0;
        float max_val = step_logits[0];
        for (size_t c = 1; c < num_classes; ++c) {
            if (step_logits[c] > max_val) {
                max_val = step_logits[c];
                best_c = c;
            }
        }

        // CTC 规则：跳过 0 占位符 ('#')，且去除连续重复字符
        if (best_c != 0 && best_c != pre_idx && best_c < NUM_PLATE_CHARS) {
            plate_no += PLATE_NAMES[best_c];
            float exp_sum = 0.0f;
            for (size_t c = 0; c < num_classes; ++c) {
                exp_sum += std::exp(step_logits[c] - max_val);
            }
            if (exp_sum > 0.0f && std::isfinite(exp_sum)) {
                confidence_sum += 1.0f / exp_sum;
                ++recognized_chars;
            }
        }
        pre_idx = best_c;
    }

    if (plate_no.empty() || recognized_chars == 0) {
        return {};
    }
    res.plate_number = plate_no;
    res.score = confidence_sum / static_cast<float>(recognized_chars);

    if (outputs.size() > 1 && outputs[1].element_count() >= NUM_COLORS) {
        const float* color_logits = outputs[1].data<float>();
        if (color_logits) {
            size_t best_color = 0;
            float max_color_val = color_logits[0];
            for (size_t i = 1; i < NUM_COLORS; ++i) {
                if (color_logits[i] > max_color_val) {
                    max_color_val = color_logits[i];
                    best_color = i;
                }
            }
            res.plate_color = COLOR_NAMES[best_color];
        }
    }

    return res;
}

} // namespace zhulong::plate
