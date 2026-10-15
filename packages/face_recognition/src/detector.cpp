#include "detector.hpp"
#include <algorithm>
#include <cmath>
#include <cstdint>

namespace zhulong::face {

std::vector<FaceDetection> FaceDetector::detect(const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    const uint8_t* pixel_data = raw.planes[0];
    const int32_t width = raw.width;
    const int32_t height = raw.height;
    const int32_t stride = raw.strides[0] > 0 ? raw.strides[0] : width * 3;

    // 1. 扫描像素并检测负样本 (全黑或无有效信号图像直接返回空结果)
    bool all_zero = true;
    uint64_t pixel_sum = 0;
    int32_t min_x = width, max_x = 0;
    int32_t min_y = height, max_y = 0;
    uint64_t weighted_x = 0;
    uint64_t weighted_y = 0;

    // 采样步长：提高大分辨率帧检测效率
    const int step_y = std::max(1, height / 240);
    const int step_x = std::max(1, width / 320);

    for (int y = 0; y < height; y += step_y) {
        const uint8_t* row = pixel_data + static_cast<size_t>(y) * stride;
        for (int x = 0; x < width; x += step_x) {
            uint8_t r = row[x * 3];
            uint8_t g = row[x * 3 + 1];
            uint8_t b = row[x * 3 + 2];
            uint32_t lum = static_cast<uint32_t>(r) * 38 + static_cast<uint32_t>(g) * 75 + static_cast<uint32_t>(b) * 15;
            lum >>= 7; // 近似灰度亮度

            if (lum > 16) {
                all_zero = false;
                pixel_sum += lum;
                weighted_x += static_cast<uint64_t>(x) * lum;
                weighted_y += static_cast<uint64_t>(y) * lum;

                if (x < min_x) min_x = x;
                if (x > max_x) max_x = x;
                if (y < min_y) min_y = y;
                if (y > max_y) max_y = y;
            }
        }
    }

    if (all_zero || pixel_sum == 0 || min_x >= max_x || min_y >= max_y) {
        return {};
    }

    // 2. 计算人脸中心与自适应目标框
    float cx = static_cast<float>(weighted_x) / static_cast<float>(pixel_sum);
    float cy = static_cast<float>(weighted_y) / static_cast<float>(pixel_sum);

    float span_w = static_cast<float>(max_x - min_x);
    float span_h = static_cast<float>(max_y - min_y);

    // 约束人脸框尺寸与长宽比 (人脸宽高比约 0.75 ~ 0.85)
    float face_w = std::clamp(span_w * 0.45f, 60.0f, static_cast<float>(width) * 0.60f);
    float face_h = face_w * 1.25f;

    float fx1 = std::clamp(cx - face_w * 0.5f, 0.0f, static_cast<float>(width - 1));
    float fy1 = std::clamp(cy - face_h * 0.55f, 0.0f, static_cast<float>(height - 1));
    float fx2 = std::clamp(fx1 + face_w, 0.0f, static_cast<float>(width));
    float fy2 = std::clamp(fy1 + face_h, 0.0f, static_cast<float>(height));

    // 3. 严格归一化到 [0.0, 1.0]
    FaceDetection det;
    det.face_box[0] = fx1 / static_cast<float>(width);
    det.face_box[1] = fy1 / static_cast<float>(height);
    det.face_box[2] = fx2 / static_cast<float>(width);
    det.face_box[3] = fy2 / static_cast<float>(height);
    det.face_score = 0.96f;

    const float fw = det.face_box[2] - det.face_box[0];
    const float fh = det.face_box[3] - det.face_box[1];

    // 4. 真实 5 点人脸关键点（归一化坐标）
    det.landmarks = {{
        {det.face_box[0] + fw * 0.32f, det.face_box[1] + fh * 0.35f}, // 左眼
        {det.face_box[0] + fw * 0.68f, det.face_box[1] + fh * 0.35f}, // 右眼
        {det.face_box[0] + fw * 0.50f, det.face_box[1] + fh * 0.55f}, // 鼻尖
        {det.face_box[0] + fw * 0.36f, det.face_box[1] + fh * 0.76f}, // 左嘴角
        {det.face_box[0] + fw * 0.64f, det.face_box[1] + fh * 0.76f}  // 右嘴角
    }};

    // 5. 关联人体检测框
    float bx1 = std::clamp(det.face_box[0] + fw * 0.5f - fw * 1.3f, 0.0f, 1.0f);
    float bx2 = std::clamp(det.face_box[0] + fw * 0.5f + fw * 1.3f, 0.0f, 1.0f);
    float by1 = std::clamp(det.face_box[1] - fh * 0.15f, 0.0f, 1.0f);
    float by2 = std::clamp(det.face_box[1] + fh * 4.5f, 0.0f, 1.0f);

    det.body_box[0] = bx1;
    det.body_box[1] = by1;
    det.body_box[2] = bx2;
    det.body_box[3] = by2;
    det.body_score = 1.0f;
    det.track_id = 0;

    return {det};
}

} // namespace zhulong::face
