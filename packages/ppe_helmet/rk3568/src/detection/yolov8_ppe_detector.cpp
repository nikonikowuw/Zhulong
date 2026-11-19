#include "detection/yolov8_ppe_detector.hpp"

#include "common/runner_log.hpp"

#include <array>
#include <string>

namespace zhulong::ppe {
namespace {

constexpr char kModule[] = "ppe_helmet";
constexpr float kInv255 = 1.0f / 255.0f;

bool image_usable(const zhulong_frame_view& raw) noexcept {
    return raw.width > 0 && raw.height > 0 && raw.planes[0] != nullptr;
}

bool output_matches(const zhulong::Tensor& tensor, int64_t channels, int64_t grid_h, int64_t grid_w) {
    if (tensor.dtype() != zhulong::DataType::FLOAT32) {
        return false;
    }
    const auto& shape = tensor.shape();
    return shape.size() == 4 && shape[0] == 1 && shape[1] == channels && shape[2] == grid_h &&
           shape[3] == grid_w;
}

const zhulong::Tensor* find_output(const std::vector<zhulong::Tensor>& outputs, int64_t channels,
                                   int64_t grid_h, int64_t grid_w) {
    for (const auto& output : outputs) {
        if (output_matches(output, channels, grid_h, grid_w)) {
            return &output;
        }
    }
    return nullptr;
}

std::string describe_outputs(const std::vector<zhulong::Tensor>& outputs) {
    std::string detail = "outputs=" + std::to_string(outputs.size());
    for (const auto& output : outputs) {
        detail += " ";
        detail += zhulong::data_type_name(output.dtype());
        detail += "[";
        const auto& shape = output.shape();
        for (size_t i = 0; i < shape.size(); ++i) {
            if (i != 0) {
                detail += ",";
            }
            detail += std::to_string(shape[i]);
        }
        detail += "]";
    }
    return detail;
}

#if !defined(ZHULONG_INFERENCE_RKNN)
/** @brief HWC RGB24 UINT8 -> NCHW FLOAT32（/255），供 ONNX 档使用 */
void hwc_to_nchw_normalized(const uint8_t* source, float* destination) {
    const size_t pixel_count = static_cast<size_t>(kPpeModelWidth) * kPpeModelHeight;
    for (size_t pixel = 0; pixel < pixel_count; ++pixel) {
        const size_t base = pixel * 3;
        destination[pixel] = static_cast<float>(source[base]) * kInv255;
        destination[pixel_count + pixel] = static_cast<float>(source[base + 1]) * kInv255;
        destination[pixel_count * 2 + pixel] = static_cast<float>(source[base + 2]) * kInv255;
    }
}
#endif

} // namespace

PpeDetectorWorkspace::PpeDetectorWorkspace()
    : letterboxed_({kPpeModelHeight, kPpeModelWidth, 3}, zhulong::DataType::UINT8) {
#if defined(ZHULONG_INFERENCE_RKNN)
    // RKNN 模型内嵌 mean=0/std=255，直接喂原始 RGB uint8 NHWC；量化/归一化由 NPU 完成。
    inputs_.emplace_back(std::vector<int64_t>{1, kPpeModelHeight, kPpeModelWidth, 3},
                         zhulong::DataType::UINT8);
#else
    // ONNX 档：张量内存随模型 shape 固定为 NCHW FLOAT32。
    inputs_.emplace_back(std::vector<int64_t>{1, 3, kPpeModelHeight, kPpeModelWidth},
                         zhulong::DataType::FLOAT32);
#endif
}

zhulong::Tensor& PpeDetectorWorkspace::letterbox_target() noexcept {
#if defined(ZHULONG_INFERENCE_RKNN)
    return inputs_.front();
#else
    return letterboxed_;
#endif
}

zhulong::Tensor& PpeDetectorWorkspace::model_input() noexcept {
    return inputs_.front();
}

bool PpeDetectorWorkspace::should_report_geometry_mismatch() noexcept {
    if (geometry_mismatch_reported_) {
        return false;
    }
    geometry_mismatch_reported_ = true;
    return true;
}

bool PpeDetectorWorkspace::should_report_layout_mismatch() noexcept {
    if (layout_mismatch_reported_) {
        return false;
    }
    layout_mismatch_reported_ = true;
    return true;
}

const std::vector<zhulong::Detection>& Yolov8PpeDetector::detect(
    zhulong::Session& session, const zhulong::Image& image, float score_threshold,
    float nms_threshold, PpeDetectorWorkspace& workspace) const {
    PpeDecodeBuffers& buffers = workspace.decode_buffers();
    buffers.detections.clear();

    const auto& raw = image.raw_view();
    if (!session.valid() || !image_usable(raw)) {
        return buffers.detections;
    }

    zhulong::LetterboxParams letterbox;
    letterbox.target_width = kPpeModelWidth;
    letterbox.target_height = kPpeModelHeight;
    letterbox.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    const zhulong_status_t letterbox_status =
        zhulong::ops::letterbox(image, workspace.letterbox_target(), letterbox);
    if (letterbox_status != ZHULONG_PLUGIN_OK) {
        if (workspace.should_report_geometry_mismatch()) {
            log_warning(kModule, "letterbox failed",
                        "status=" + std::to_string(static_cast<int>(letterbox_status)));
        }
        return buffers.detections;
    }
    if (!(letterbox.scale > 0.0f)) {
        if (workspace.should_report_geometry_mismatch()) {
            log_warning(kModule, "letterbox geometry invalid",
                        "scale=" + std::to_string(letterbox.scale) +
                            " pad_left=" + std::to_string(letterbox.pad_left) +
                            " pad_top=" + std::to_string(letterbox.pad_top));
        }
        return buffers.detections;
    }

#if !defined(ZHULONG_INFERENCE_RKNN)
    hwc_to_nchw_normalized(workspace.letterboxed_tensor().data<uint8_t>(),
                           workspace.model_input().data<float>());
#endif

    const std::vector<zhulong::Tensor> outputs = session.forward(workspace.model_inputs());
    if (outputs.size() < kPpeStrides.size() * 2) {
        if (workspace.should_report_layout_mismatch()) {
            log_warning(kModule, "model output layout mismatch", describe_outputs(outputs));
        }
        return buffers.detections;
    }

    std::vector<Yolov8PpeHeadView>& heads = workspace.heads();
    heads.clear();
    for (const int stride : kPpeStrides) {
        const int grid_h = kPpeModelHeight / stride;
        const int grid_w = kPpeModelWidth / stride;
        const zhulong::Tensor* box_output = find_output(outputs, 64, grid_h, grid_w);
        const zhulong::Tensor* score_output = find_output(outputs, kClassCount, grid_h, grid_w);
        if (box_output == nullptr || score_output == nullptr) {
            if (workspace.should_report_layout_mismatch()) {
                log_warning(kModule, "model output layout mismatch",
                            "stride=" + std::to_string(stride) + " " + describe_outputs(outputs));
            }
            return buffers.detections;
        }
        Yolov8PpeHeadView head;
        head.box = box_output->data<float>();
        head.score = score_output->data<float>();
        head.grid_h = grid_h;
        head.grid_w = grid_w;
        head.stride_h = kPpeModelHeight / grid_h;
        head.stride_w = kPpeModelWidth / grid_w;
        heads.push_back(head);
    }

    Yolov8PpeGeometry geometry;
    geometry.scale = letterbox.scale;
    geometry.pad_left = letterbox.pad_left;
    geometry.pad_top = letterbox.pad_top;
    geometry.image_width = raw.width;
    geometry.image_height = raw.height;

    decode_yolov8_ppe(heads, geometry, score_threshold, nms_threshold, buffers);
    return buffers.detections;
}

} // namespace zhulong::ppe
