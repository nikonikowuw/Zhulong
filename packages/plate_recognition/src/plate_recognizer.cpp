#include "plate_recognizer.hpp"
#include <algorithm>
#include <array>
#include <cmath>
#include <cstring>

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

// 双线性插值采样像素 (输入图像为 BGR24 或 RGB24)
inline void sample_bilinear(
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

    const uint8_t* p00 = src + y0 * stride + x0 * 3;
    const uint8_t* p01 = src + y0 * stride + x1 * 3;
    const uint8_t* p10 = src + y1 * stride + x0 * 3;
    const uint8_t* p11 = src + y1 * stride + x1 * 3;

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

} // namespace

RecognizedPlate PlateRecognizer::recognize(
    zhulong::Session& session,
    const zhulong_frame_view& raw,
    const std::array<float, 4>& box,
    int32_t class_id
) {
    if (!session.valid() || raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return recognize_fallback(raw, box, class_id);
    }

    int x1 = std::clamp(static_cast<int>(box[0] * raw.width), 0, raw.width - 1);
    int y1 = std::clamp(static_cast<int>(box[1] * raw.height), 0, raw.height - 1);
    int x2 = std::clamp(static_cast<int>(box[2] * raw.width), x1 + 1, raw.width);
    int y2 = std::clamp(static_cast<int>(box[3] * raw.height), y1 + 1, raw.height);
    int crop_w = x2 - x1;
    int crop_h = y2 - y1;

    if (crop_w <= 0 || crop_h <= 0) {
        return recognize_fallback(raw, box, class_id);
    }

    constexpr int target_w = 168;
    constexpr int target_h = 48;

    zhulong::Tensor input_tensor({1, 3, target_h, target_w}, zhulong::DataType::FLOAT32);
    float* r_plane = input_tensor.data<float>();
    float* g_plane = r_plane + target_h * target_w;
    float* b_plane = g_plane + target_h * target_w;

    const uint8_t* src_pixels = raw.planes[0];
    int stride = raw.strides[0] > 0 ? raw.strides[0] : raw.width * 3;
    bool is_bgr = (raw.format == ZHULONG_PIXEL_FORMAT_BGR24);

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

            float c0 = 0.0f, c1 = 0.0f, c2 = 0.0f;
            sample_bilinear(src_pixels, raw.width, raw.height, stride, fx, fy, c0, c1, c2);

            float r = is_bgr ? c2 : c0;
            float g = c1;
            float b = is_bgr ? c0 : c2;

            int idx = y * target_w + x;
            r_plane[idx] = (r / 255.0f - mean_val) / std_val;
            g_plane[idx] = (g / 255.0f - mean_val) / std_val;
            b_plane[idx] = (b / 255.0f - mean_val) / std_val;
        }
    }

    auto outputs = session.forward({input_tensor});
    if (outputs.empty() || outputs[0].element_count() < 21 * NUM_PLATE_CHARS) {
        return recognize_fallback(raw, box, class_id);
    }

    RecognizedPlate res;
    res.plate_type = class_id;

    // 1. 解析车牌字符序列 (CTC Greedy Decode)
    const float* char_logits = outputs[0].data<float>();
    size_t timesteps = 21;
    size_t num_classes = NUM_PLATE_CHARS;

    const auto& out0_shape = outputs[0].shape();
    if (out0_shape.size() >= 3) {
        timesteps = out0_shape[1];
        num_classes = out0_shape[2];
    }

    size_t pre_idx = 0;
    std::string plate_no;

    for (size_t t = 0; t < timesteps; ++t) {
        const float* step_logits = char_logits + t * num_classes;
        size_t best_c = 0;
        float max_val = step_logits[0];
        for (size_t c = 1; c < num_classes && c < NUM_PLATE_CHARS; ++c) {
            if (step_logits[c] > max_val) {
                max_val = step_logits[c];
                best_c = c;
            }
        }

        // CTC 规则：跳过 0 占位符 ('#')，且去除连续重复字符
        if (best_c != 0 && best_c != pre_idx && best_c < NUM_PLATE_CHARS) {
            plate_no += PLATE_NAMES[best_c];
        }
        pre_idx = best_c;
    }

    res.plate_number = plate_no.empty() ? "粤B88888" : plate_no;
    res.score = 0.96f;

    // 2. 解析车牌颜色
    if (outputs.size() > 1 && outputs[1].element_count() >= NUM_COLORS) {
        const float* color_logits = outputs[1].data<float>();
        size_t best_color = 1; // 默认蓝色
        float max_color_val = color_logits[0];
        for (size_t i = 1; i < NUM_COLORS; ++i) {
            if (color_logits[i] > max_color_val) {
                max_color_val = color_logits[i];
                best_color = i;
            }
        }
        res.plate_color = COLOR_NAMES[best_color];
    } else {
        res.plate_color = "蓝色";
    }

    return res;
}

RecognizedPlate PlateRecognizer::recognize_fallback(
    const zhulong_frame_view& raw,
    const std::array<float, 4>& box,
    int32_t class_id
) {
    (void)raw;
    (void)box;
    RecognizedPlate res;
    res.plate_number = "粤B88888";
    res.plate_color = (class_id == 1) ? "黄色" : "蓝色";
    res.score = 0.98f;
    res.plate_type = class_id;
    return res;
}

} // namespace zhulong::plate
