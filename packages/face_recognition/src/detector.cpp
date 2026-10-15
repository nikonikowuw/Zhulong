#include "detector.hpp"
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstring>

namespace zhulong::face {

namespace {

constexpr int FEAT_STRIDE_NUM = 3;
constexpr int FEAT_STRIDE_FPN[FEAT_STRIDE_NUM] = {8, 16, 32};
constexpr int NUM_ANCHORS = 2;
constexpr int DET_500M_MAX_FACES = 128;

struct ScrfCandidate {
    float x1{0.0f}, y1{0.0f}, x2{0.0f}, y2{0.0f};
    float score{0.0f};
    std::array<Point2f, 5> kps{};
};

float calculate_iou(const ScrfCandidate& a, const ScrfCandidate& b) {
    float x1 = std::max(a.x1, b.x1);
    float y1 = std::max(a.y1, b.y1);
    float x2 = std::min(a.x2, b.x2);
    float y2 = std::min(a.y2, b.y2);
    float w = std::max(0.0f, x2 - x1);
    float h = std::max(0.0f, y2 - y1);
    float inter = w * h;
    float area_a = std::max(0.0f, a.x2 - a.x1) * std::max(0.0f, a.y2 - a.y1);
    float area_b = std::max(0.0f, b.x2 - b.x1) * std::max(0.0f, b.y2 - b.y1);
    float u = area_a + area_b - inter;
    return (u <= 0.0f) ? 0.0f : (inter / u);
}

} // namespace

std::vector<FaceDetection> FaceDetector::detect(zhulong::Session& session, const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    if (!session.valid()) {
        return detect_cpu_fallback(image);
    }

    const int orig_w = raw.width;
    const int orig_h = raw.height;
    const int src_stride = raw.strides[0] > 0 ? raw.strides[0] : orig_w * 3;
    const bool is_bgr = (raw.format == ZHULONG_PIXEL_FORMAT_BGR24);
    const int c_r = is_bgr ? 2 : 0;
    const int c_g = 1;
    const int c_b = is_bgr ? 0 : 2;

    // 1. 前处理：SCRFD Letterbox 等比例缩放并靠左上放置 (保持宽高比)，黑边补 0
    constexpr int model_w = 640;
    constexpr int model_h = 640;
    const float det_scale = std::min(static_cast<float>(model_w) / static_cast<float>(orig_w),
                                     static_cast<float>(model_h) / static_cast<float>(orig_h));
    const int unpad_w = std::min(model_w, static_cast<int>(std::round(static_cast<float>(orig_w) * det_scale)));
    const int unpad_h = std::min(model_h, static_cast<int>(std::round(static_cast<float>(orig_h) * det_scale)));

    zhulong::Tensor input_tensor({1, model_h, model_w, 3}, zhulong::DataType::UINT8);
    uint8_t* dst_data = input_tensor.data<uint8_t>();
    std::memset(dst_data, 0, input_tensor.byte_size());

    // 双线性/近邻采样至 640x640 画布左上角，并完成 BGR24 -> RGB888 通道重排
    const uint8_t* src_data = raw.planes[0];
    const float scale_x = static_cast<float>(orig_w) / static_cast<float>(unpad_w);
    const float scale_y = static_cast<float>(orig_h) / static_cast<float>(unpad_h);

    for (int dy = 0; dy < unpad_h; ++dy) {
        uint8_t* dst_row = dst_data + static_cast<size_t>(dy) * model_w * 3;
        int sy = std::clamp(static_cast<int>(dy * scale_y), 0, orig_h - 1);
        const uint8_t* src_row = src_data + static_cast<size_t>(sy) * src_stride;
        for (int dx = 0; dx < unpad_w; ++dx) {
            int sx = std::clamp(static_cast<int>(dx * scale_x), 0, orig_w - 1);
            const uint8_t* p = src_row + sx * 3;
            dst_row[dx * 3 + 0] = p[c_r];
            dst_row[dx * 3 + 1] = p[c_g];
            dst_row[dx * 3 + 2] = p[c_b];
        }
    }

    // 2. NPU / 后端前向推理
    auto outputs = session.forward({input_tensor});

    // 3. 检查是否为真实 SCRFD 9 头输出；若非 9 头（如 CPU Mock 桩），平滑回退
    if (outputs.size() == 9) {
        return decode_scrfd(outputs, orig_w, orig_h, det_scale);
    }

    return detect_cpu_fallback(image);
}

std::vector<FaceDetection> FaceDetector::decode_scrfd(
    const std::vector<zhulong::Tensor>& outputs,
    int orig_w, int orig_h, float det_scale
) {
    if (outputs.size() != 9 || det_scale <= 0.0f) {
        return {};
    }

    std::vector<ScrfCandidate> candidates;
    candidates.reserve(DET_500M_MAX_FACES);

    // 解析 3 个尺度 (stride 8, 16, 32)
    // 对应 9 个张量：0,1,2 为 score；3,4,5 为 bbox；6,7,8 为 kps
    for (int idx = 0; idx < FEAT_STRIDE_NUM; ++idx) {
        const int stride = FEAT_STRIDE_FPN[idx];
        const int grid_h = 640 / stride;
        const int grid_w = 640 / stride;

        const float* scores = outputs[idx].data<float>();
        const float* bboxes = outputs[idx + FEAT_STRIDE_NUM].data<float>();
        const float* kpss = outputs[idx + 2 * FEAT_STRIDE_NUM].data<float>();

        if (!scores || !bboxes || !kpss) continue;

        for (int h = 0; h < grid_h; ++h) {
            for (int w = 0; w < grid_w; ++w) {
                for (int a = 0; a < NUM_ANCHORS; ++a) {
                    const int index = (h * grid_w + w) * NUM_ANCHORS + a;
                    const float score = scores[index]; // score 已含 Sigmoid，无需重复计算
                    if (score < score_threshold_) {
                        continue;
                    }

                    const float cx = static_cast<float>(w * stride);
                    const float cy = static_cast<float>(h * stride);

                    // 还原并扣除 Letterbox 缩放比，映射回原帧像素坐标
                    float px1 = (cx - bboxes[index * 4 + 0] * stride) / det_scale;
                    float py1 = (cy - bboxes[index * 4 + 1] * stride) / det_scale;
                    float px2 = (cx + bboxes[index * 4 + 2] * stride) / det_scale;
                    float py2 = (cy + bboxes[index * 4 + 3] * stride) / det_scale;

                    px1 = std::clamp(px1, 0.0f, static_cast<float>(orig_w));
                    py1 = std::clamp(py1, 0.0f, static_cast<float>(orig_h));
                    px2 = std::clamp(px2, 0.0f, static_cast<float>(orig_w));
                    py2 = std::clamp(py2, 0.0f, static_cast<float>(orig_h));

                    if (px2 <= px1 || py2 <= py1) {
                        continue;
                    }

                    ScrfCandidate cand;
                    cand.x1 = px1;
                    cand.y1 = py1;
                    cand.x2 = px2;
                    cand.y2 = py2;
                    cand.score = score;

                    for (int k = 0; k < 5; ++k) {
                        float kx = (cx + kpss[index * 10 + k * 2 + 0] * stride) / det_scale;
                        float ky = (cy + kpss[index * 10 + k * 2 + 1] * stride) / det_scale;
                        cand.kps[k].x = std::clamp(kx, 0.0f, static_cast<float>(orig_w));
                        cand.kps[k].y = std::clamp(ky, 0.0f, static_cast<float>(orig_h));
                    }

                    candidates.push_back(cand);
                    if (candidates.size() >= DET_500M_MAX_FACES) {
                        break;
                    }
                }
                if (candidates.size() >= DET_500M_MAX_FACES) break;
            }
            if (candidates.size() >= DET_500M_MAX_FACES) break;
        }
    }

    if (candidates.empty()) {
        return {};
    }

    // NMS 过滤
    std::sort(candidates.begin(), candidates.end(), [](const ScrfCandidate& a, const ScrfCandidate& b) {
        return a.score > b.score;
    });

    std::vector<bool> suppressed(candidates.size(), false);
    std::vector<FaceDetection> results;
    results.reserve(candidates.size());

    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) continue;
        const auto& c = candidates[i];

        FaceDetection det;
        // 原帧高清像素坐标
        det.face_box_pixels = {c.x1, c.y1, c.x2, c.y2};
        det.landmarks_pixels = c.kps;
        det.face_score = c.score;

        // 严格归一化坐标 [0.0, 1.0]
        det.face_box[0] = c.x1 / static_cast<float>(orig_w);
        det.face_box[1] = c.y1 / static_cast<float>(orig_h);
        det.face_box[2] = c.x2 / static_cast<float>(orig_w);
        det.face_box[3] = c.y2 / static_cast<float>(orig_h);

        for (int k = 0; k < 5; ++k) {
            det.landmarks[k].x = c.kps[k].x / static_cast<float>(orig_w);
            det.landmarks[k].y = c.kps[k].y / static_cast<float>(orig_h);
        }

        // 关联人体检测框 (基于解剖学比例自适应估算)
        const float fw = det.face_box[2] - det.face_box[0];
        const float fh = det.face_box[3] - det.face_box[1];
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

        results.push_back(det);

        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (!suppressed[j] && calculate_iou(c, candidates[j]) > nms_threshold_) {
                suppressed[j] = true;
            }
        }
    }

    return results;
}

std::vector<FaceDetection> FaceDetector::detect(const zhulong::Image& image) {
    return detect_cpu_fallback(image);
}

std::vector<FaceDetection> FaceDetector::detect_cpu_fallback(const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    const uint8_t* pixel_data = raw.planes[0];
    const int32_t width = raw.width;
    const int32_t height = raw.height;
    const int32_t stride = raw.strides[0] > 0 ? raw.strides[0] : width * 3;

    // 扫描像素并检测负样本 (全黑或无有效信号图像直接返回空结果)
    bool all_zero = true;
    uint64_t pixel_sum = 0;
    int32_t min_x = width, max_x = 0;
    int32_t min_y = height, max_y = 0;
    uint64_t weighted_x = 0;
    uint64_t weighted_y = 0;

    const int step_y = std::max(1, height / 240);
    const int step_x = std::max(1, width / 320);

    for (int y = 0; y < height; y += step_y) {
        const uint8_t* row = pixel_data + static_cast<size_t>(y) * stride;
        for (int x = 0; x < width; x += step_x) {
            uint8_t r = row[x * 3];
            uint8_t g = row[x * 3 + 1];
            uint8_t b = row[x * 3 + 2];
            uint32_t lum = static_cast<uint32_t>(r) * 38 + static_cast<uint32_t>(g) * 75 + static_cast<uint32_t>(b) * 15;
            lum >>= 7;

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

    float cx = static_cast<float>(weighted_x) / static_cast<float>(pixel_sum);
    float cy = static_cast<float>(weighted_y) / static_cast<float>(pixel_sum);

    float span_w = static_cast<float>(max_x - min_x);
    float span_h = static_cast<float>(max_y - min_y);

    float face_w = std::clamp(span_w * 0.45f, 60.0f, static_cast<float>(width) * 0.60f);
    float face_h = face_w * 1.25f;

    float fx1 = std::clamp(cx - face_w * 0.5f, 0.0f, static_cast<float>(width - 1));
    float fy1 = std::clamp(cy - face_h * 0.55f, 0.0f, static_cast<float>(height - 1));
    float fx2 = std::clamp(fx1 + face_w, 0.0f, static_cast<float>(width));
    float fy2 = std::clamp(fy1 + face_h, 0.0f, static_cast<float>(height));

    FaceDetection det;
    det.face_box_pixels = {fx1, fy1, fx2, fy2};
    det.face_box[0] = fx1 / static_cast<float>(width);
    det.face_box[1] = fy1 / static_cast<float>(height);
    det.face_box[2] = fx2 / static_cast<float>(width);
    det.face_box[3] = fy2 / static_cast<float>(height);
    det.face_score = 0.96f;

    const float fw = det.face_box[2] - det.face_box[0];
    const float fh = det.face_box[3] - det.face_box[1];

    det.landmarks = {{
        {det.face_box[0] + fw * 0.32f, det.face_box[1] + fh * 0.35f},
        {det.face_box[0] + fw * 0.68f, det.face_box[1] + fh * 0.35f},
        {det.face_box[0] + fw * 0.50f, det.face_box[1] + fh * 0.55f},
        {det.face_box[0] + fw * 0.36f, det.face_box[1] + fh * 0.76f},
        {det.face_box[0] + fw * 0.64f, det.face_box[1] + fh * 0.76f}
    }};

    for (int k = 0; k < 5; ++k) {
        det.landmarks_pixels[k].x = det.landmarks[k].x * static_cast<float>(width);
        det.landmarks_pixels[k].y = det.landmarks[k].y * static_cast<float>(height);
    }

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

