#include <algorithm/runtime/runtime_factory.hpp>
#include <algorithm/runtime/vision_backend.hpp>
#include <cassert>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <vector>

static zhulong_status_t run_letterbox(zhulong::IVisionBackend& backend, const uint8_t* pixels,
                                      int width, int height, int stride, zhulong_pixel_format_t format,
                                      zhulong_pixel_format_t target_format, int target_width,
                                      int target_height, zhulong::Tensor& output,
                                      zhulong::LetterboxParams* output_params = nullptr) {
    zhulong_frame_view frame{};
    frame.width = width;
    frame.height = height;
    frame.format = format;
    frame.planes[0] = pixels;
    frame.strides[0] = stride;

    zhulong::LetterboxParams params;
    params.target_width = target_width;
    params.target_height = target_height;
    params.target_format = target_format;
    zhulong_status_t rc = backend.letterbox(frame, output, params);
    if (output_params) *output_params = params;
    return rc;
}

int main() {
    // 1. Tensor dimensions must describe a fixed, representable allocation.
    zhulong::Tensor empty_tensor;
    assert(empty_tensor.element_count() == 0);

    zhulong::Tensor fixed_tensor({2, 3}, zhulong::DataType::UINT8);
    assert(fixed_tensor.element_count() == 6);
    assert(fixed_tensor.byte_size() == 6);

    // 1.1 Tensor DMA-BUF binding
    uint8_t dummy_dma_mem[32] = {0};
    zhulong::Tensor dma_tensor = zhulong::Tensor::from_dma_buf(42, {2, 4}, zhulong::DataType::UINT8, dummy_dma_mem);
    assert(dma_tensor.dma_buf_fd() == 42);
    assert(dma_tensor.is_external());
    assert(dma_tensor.element_count() == 8);
    assert(dma_tensor.byte_size() == 8);
    assert(dma_tensor.raw_data() == dummy_dma_mem);

    for (const std::vector<int64_t>& dims : {std::vector<int64_t>{1, 0, 3},
                                             std::vector<int64_t>{1, -1, 3}}) {
        bool rejected = false;
        try {
            zhulong::Tensor invalid_tensor(dims, zhulong::DataType::UINT8);
        } catch (const std::invalid_argument&) {
            rejected = true;
        }
        assert(rejected);
    }
    bool overflow_rejected = false;
    try {
        zhulong::Tensor oversized_tensor({std::numeric_limits<int64_t>::max(),
                                          std::numeric_limits<int64_t>::max()},
                                         zhulong::DataType::UINT8);
    } catch (const std::length_error&) {
        overflow_rejected = true;
    }
    assert(overflow_rejected);

    // 2. Test inference backend.
    auto infer_backend = zhulong::get_default_inference_backend();
    assert(infer_backend != nullptr);

    auto model = infer_backend->load_model("{\"model_path\": \"mock.model\"}");
    assert(model != nullptr);

    auto session = infer_backend->create_session(model, 1);
    assert(session != nullptr);

    zhulong::Tensor input_tensor({1, 3, 640, 640}, zhulong::DataType::FLOAT32);
    input_tensor.data<float>()[0] = 0.5f;

    std::vector<zhulong::Tensor> outputs;
    zhulong_status_t rc = infer_backend->forward(session.get(), {input_tensor}, outputs);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(outputs.size() == 1);
    assert(outputs[0].shape().size() == 2);
    assert(outputs[0].shape()[0] == 1 && outputs[0].shape()[1] == 6);

    const float* out_data = outputs[0].data<float>();
    assert(out_data[4] > 0.8f);

    // 3. Padded BGR rows must be read by stride and converted to RGB.
    auto vision_backend = zhulong::get_default_vision_backend();
    assert(vision_backend != nullptr);

    const std::vector<uint8_t> padded_bgr = {
        0, 0, 255, 0, 255, 0, 90, 91,
        255, 0, 0, 255, 255, 255, 92, 93
    };
    zhulong::Tensor letterbox_out;
    zhulong::LetterboxParams params;
    rc = run_letterbox(*vision_backend, padded_bgr.data(), 2, 2, 8,
                       ZHULONG_PIXEL_FORMAT_BGR24, ZHULONG_PIXEL_FORMAT_RGB24,
                       2, 2, letterbox_out, &params);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(letterbox_out.shape() == std::vector<int64_t>({2, 2, 3}));
    assert(params.scale == 1.0f && params.pad_left == 0 && params.pad_top == 0);
    const uint8_t* pixels = letterbox_out.data<uint8_t>();
    assert(pixels[0] == 255 && pixels[1] == 0 && pixels[2] == 0);
    assert(pixels[3] == 0 && pixels[4] == 255 && pixels[5] == 0);
    assert(pixels[6] == 0 && pixels[7] == 0 && pixels[8] == 255);

    // Zero stride remains compatible with tightly packed legacy callers; target format conversion works.
    const uint8_t rgb_pixel[] = {10, 20, 30};
    rc = run_letterbox(*vision_backend, rgb_pixel, 1, 1, 0,
                       ZHULONG_PIXEL_FORMAT_RGB24, ZHULONG_PIXEL_FORMAT_BGR24,
                       1, 1, letterbox_out);
    assert(rc == ZHULONG_PLUGIN_OK);
    pixels = letterbox_out.data<uint8_t>();
    assert(pixels[0] == 30 && pixels[1] == 20 && pixels[2] == 10);

    // A 2x2 -> 3x3 resize samples the center as the bilinear average of all four corners.
    const uint8_t grayscale_corners[] = {
        0, 0, 0, 100, 100, 100,
        200, 200, 200, 255, 255, 255
    };
    rc = run_letterbox(*vision_backend, grayscale_corners, 2, 2, 6,
                       ZHULONG_PIXEL_FORMAT_RGB24, ZHULONG_PIXEL_FORMAT_RGB24,
                       3, 3, letterbox_out);
    assert(rc == ZHULONG_PLUGIN_OK);
    pixels = letterbox_out.data<uint8_t>();
    const size_t center = (1 * 3 + 1) * 3;
    assert(pixels[center] == 139 && pixels[center + 1] == 139 && pixels[center + 2] == 139);

    // Letterbox writes directly into the padded destination rows.
    const uint8_t strip[] = {10, 10, 10, 20, 20, 20};
    rc = run_letterbox(*vision_backend, strip, 2, 1, 6,
                       ZHULONG_PIXEL_FORMAT_RGB24, ZHULONG_PIXEL_FORMAT_RGB24,
                       4, 4, letterbox_out, &params);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(params.pad_top == 1);
    pixels = letterbox_out.data<uint8_t>();
    assert(pixels[0] == 114);
    assert(pixels[(1 * 4) * 3] == 10);
    assert(pixels[(1 * 4 + 3) * 3] == 20);
    assert(pixels[(3 * 4) * 3] == 114);

    // Unsupported formats and invalid strides fail before source pixels are read.
    for (zhulong_pixel_format_t unsupported : {ZHULONG_PIXEL_FORMAT_NV12,
                                                ZHULONG_PIXEL_FORMAT_YUV420P,
                                                ZHULONG_PIXEL_FORMAT_RGBA32,
                                                ZHULONG_PIXEL_FORMAT_UNKNOWN}) {
        rc = run_letterbox(*vision_backend, nullptr, 2, 2, 0,
                           unsupported, ZHULONG_PIXEL_FORMAT_RGB24, 2, 2, letterbox_out);
        assert(rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    }
    rc = run_letterbox(*vision_backend, nullptr, 1, 1, 0,
                       ZHULONG_PIXEL_FORMAT_RGB24, ZHULONG_PIXEL_FORMAT_RGBA32,
                       1, 1, letterbox_out);
    assert(rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    rc = run_letterbox(*vision_backend, padded_bgr.data(), 2, 2, -1,
                       ZHULONG_PIXEL_FORMAT_BGR24, ZHULONG_PIXEL_FORMAT_RGB24,
                       2, 2, letterbox_out);
    assert(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT);
    rc = run_letterbox(*vision_backend, padded_bgr.data(), 2, 2, 5,
                       ZHULONG_PIXEL_FORMAT_BGR24, ZHULONG_PIXEL_FORMAT_RGB24,
                       2, 2, letterbox_out);
    assert(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT);

    // 4. Test NMS.
    std::vector<zhulong_bbox> boxes(3);
    boxes[0].box[0] = 0.10f; boxes[0].box[1] = 0.10f; boxes[0].box[2] = 0.30f; boxes[0].box[3] = 0.30f; boxes[0].confidence = 0.9f;
    boxes[1].box[0] = 0.11f; boxes[1].box[1] = 0.11f; boxes[1].box[2] = 0.31f; boxes[1].box[3] = 0.31f; boxes[1].confidence = 0.8f;
    boxes[2].box[0] = 0.60f; boxes[2].box[1] = 0.60f; boxes[2].box[2] = 0.80f; boxes[2].box[3] = 0.80f; boxes[2].confidence = 0.7f;

    auto kept_boxes = zhulong::nms(boxes, 0.45f);
    assert(kept_boxes.size() == 2);
    assert(kept_boxes[0].confidence == 0.9f);
    assert(kept_boxes[1].confidence == 0.7f);

    std::cout << "Algorithm runtime tests passed successfully!" << std::endl;
    return 0;
}
