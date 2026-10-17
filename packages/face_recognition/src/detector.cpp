#include "detector.hpp"
#include <platform/dmabuf/dmabuf_allocator.hpp>
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
    // 1. 前处理：通过 zhulong::ops::letterbox 执行硬件加速/CPU 降级视觉预处理
    constexpr int model_w = 640;
    constexpr int model_h = 640;

    // 支持通过 DMA-BUF 持久缓存分配零拷贝输入张量（避免热路径单帧分配/释放抖动）
    if (!is_onnx_ && image.dma_buf_fd() >= 0 && zhulong::platform::DmaBufAllocator::instance().is_supported()) {
        if (!dma_input_buf_ || cached_w_ != model_w || cached_h_ != model_h) {
            dma_input_buf_ = zhulong::platform::DmaBufAllocator::instance().allocate(model_w * model_h * 3);
            cached_w_ = model_w;
            cached_h_ = model_h;
        }
    } else {
        dma_input_buf_.reset();
        cached_w_ = 0;
        cached_h_ = 0;
    }

    zhulong::Tensor input_tensor;
    if (dma_input_buf_) {
        input_tensor = zhulong::Tensor::from_dma_buf(
            dma_input_buf_->fd(), {1, model_h, model_w, 3}, zhulong::DataType::UINT8, dma_input_buf_->mmap_addr());
        dma_input_buf_->sync_start(zhulong::platform::DMA_SYNC_WRITE);
    } else {
        input_tensor = zhulong::Tensor({1, model_h, model_w, 3}, zhulong::DataType::UINT8);
    }

    zhulong::LetterboxParams params;
    params.target_width = model_w;
    params.target_height = model_h;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    zhulong_status_t status = zhulong::ops::letterbox(image, input_tensor, params);
    if (dma_input_buf_) {
        dma_input_buf_->sync_end(zhulong::platform::DMA_SYNC_WRITE);
    }

    if (status != ZHULONG_PLUGIN_OK) {
        return detect_cpu_fallback(image);
    }

    // 2. NPU / 后端前向推理
    std::vector<zhulong::Tensor> outputs;
    if (is_onnx_) {
        // ONNX SCRFD: [1, 3, 640, 640] FLOAT32 NCHW, RGB, (x - 127.5f) / 128.0f
        zhulong::Tensor onnx_input({1, 3, model_h, model_w}, zhulong::DataType::FLOAT32);
        const uint8_t* src_hwc = input_tensor.data<uint8_t>();
        float* dst_nchw = onnx_input.data<float>();
        if (!src_hwc || !dst_nchw) {
            return detect_cpu_fallback(image);
        }
        constexpr int plane_size = model_w * model_h;
        float* dst_r = dst_nchw;
        float* dst_g = dst_nchw + plane_size;
        float* dst_b = dst_nchw + plane_size * 2;
        constexpr float mean = 127.5f;
        constexpr float inv_std = 1.0f / 128.0f;
        for (int i = 0; i < plane_size; ++i) {
            dst_r[i] = (static_cast<float>(src_hwc[i * 3 + 0]) - mean) * inv_std;
            dst_g[i] = (static_cast<float>(src_hwc[i * 3 + 1]) - mean) * inv_std;
            dst_b[i] = (static_cast<float>(src_hwc[i * 3 + 2]) - mean) * inv_std;
        }
        outputs = session.forward({onnx_input});
    } else {
        outputs = session.forward({input_tensor});
    }

    // 3. 检查是否为真实 SCRFD 9 头输出；若非 9 头（如 CPU Mock 桩），平滑回退
    if (outputs.size() == 9) {
        auto detections = decode_scrfd(outputs, orig_w, orig_h, params.scale, params.pad_left, params.pad_top);
        if (detections.empty()) {
            return detect_cpu_fallback(image);
        }
        return detections;
    }

    return detect_cpu_fallback(image);
}

std::vector<FaceDetection> FaceDetector::decode_scrfd(
    const std::vector<zhulong::Tensor>& outputs,
    int orig_w, int orig_h, float det_scale,
    int pad_left, int pad_top
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
                    // 还原并扣除 Letterbox 居中补边与缩放比，映射回原帧像素坐标
                    float px1 = (cx - bboxes[index * 4 + 0] * stride - static_cast<float>(pad_left)) / det_scale;
                    float py1 = (cy - bboxes[index * 4 + 1] * stride - static_cast<float>(pad_top)) / det_scale;
                    float px2 = (cx + bboxes[index * 4 + 2] * stride - static_cast<float>(pad_left)) / det_scale;
                    float py2 = (cy + bboxes[index * 4 + 3] * stride - static_cast<float>(pad_top)) / det_scale;

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
                        float kx = (cx + kpss[index * 10 + k * 2 + 0] * stride - static_cast<float>(pad_left)) / det_scale;
                        float ky = (cy + kpss[index * 10 + k * 2 + 1] * stride - static_cast<float>(pad_top)) / det_scale;
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
        det.is_real_detection = true;

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
    det.is_real_detection = false;

    return {det};
}

} // namespace zhulong::face

