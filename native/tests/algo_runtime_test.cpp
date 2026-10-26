#include <zhulong/runtime/runtime_factory.hpp>
#include <zhulong/runtime/vision_backend.hpp>
#include <cassert>
#include <cstring>
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

    // 1.2 Tensor::data<T>() 必须按 dtype 硬校验（design.md §4.8.5）：
    // ONNX 后端保留模型原始 dtype，非浮点输出被当成 float 读会静默得到垃圾值。
    zhulong::Tensor f32_tensor({2, 2}, zhulong::DataType::FLOAT32);
    f32_tensor.data<float>()[0] = 1.5f;
    assert(f32_tensor.data<float>()[0] == 1.5f);

    bool dtype_mismatch_rejected = false;
    try {
        (void)f32_tensor.data<uint8_t>();
    } catch (const std::invalid_argument&) {
        dtype_mismatch_rejected = true;
    }
    assert(dtype_mismatch_rejected);

    // 只读重载同样校验；float ↔ int64 两个方向都必须拒绝
    const zhulong::Tensor& f32_view = f32_tensor;
    bool const_dtype_mismatch_rejected = false;
    try {
        (void)f32_view.data<int64_t>();
    } catch (const std::invalid_argument&) {
        const_dtype_mismatch_rejected = true;
    }
    assert(const_dtype_mismatch_rejected);

    zhulong::Tensor i64_tensor({2}, zhulong::DataType::INT64);
    i64_tensor.data<int64_t>()[1] = 42;
    assert(i64_tensor.data<int64_t>()[1] == 42);
    bool int64_as_float_rejected = false;
    try {
        (void)i64_tensor.data<float>();
    } catch (const std::invalid_argument&) {
        int64_as_float_rejected = true;
    }
    assert(int64_as_float_rejected);

    // 2. Test inference backend.
    auto infer_backend = zhulong::get_default_inference_backend();
    assert(infer_backend != nullptr);

    // 夹具断言按**构建**分派（编译期），不做运行期 SKIP：
    // 只有 test_stub 构建才存在桩后端，产品构建（onnx/rknn）不得出现任何夹具通路。
#if defined(ZHULONG_INFERENCE_TEST_STUB)
    assert(infer_backend->load_model("models/plate_detect.onnx") == nullptr);
    assert(infer_backend->load_model("{\"model_path\": \"mock.model\"}") == nullptr);

    auto model = infer_backend->load_model("test_stub:algo_runtime_test");
    assert(model != nullptr);

    auto session = infer_backend->create_session(model, 1);
    assert(session != nullptr);

    zhulong::Tensor input_tensor({1, 3, 640, 640}, zhulong::DataType::FLOAT32);
    input_tensor.data<float>()[0] = 0.5f;

    std::vector<zhulong::Tensor> outputs;
    zhulong_status_t stub_rc = infer_backend->forward(session.get(), {input_tensor}, outputs);
    assert(stub_rc == ZHULONG_PLUGIN_OK);
    assert(outputs.size() == 1);
    assert(outputs[0].shape().size() == 2);
    assert(outputs[0].shape()[0] == 1 && outputs[0].shape()[1] == 6);

    const float* out_data = outputs[0].data<float>();
    assert(out_data[4] > 0.8f);
#else
    // 产品后端：夹具标记与 mock 规格都不是可加载的模型，必须失败。
    assert(infer_backend->load_model("test_stub:algo_runtime_test") == nullptr);
    assert(infer_backend->load_model("{\"model_path\": \"mock.model\"}") == nullptr);
#endif

    // 3. Padded BGR rows must be read by stride and converted to RGB.
    auto vision_backend = zhulong::get_default_vision_backend();
    assert(vision_backend != nullptr);

    const std::vector<uint8_t> padded_bgr = {
        0, 0, 255, 0, 255, 0, 90, 91,
        255, 0, 0, 255, 255, 255, 92, 93
    };
    zhulong::Tensor letterbox_out;
    zhulong::LetterboxParams params;
    zhulong_status_t rc = run_letterbox(*vision_backend, padded_bgr.data(), 2, 2, 8,
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

    // Unsupported formats fail before source pixels are read.
    for (zhulong_pixel_format_t unsupported : {ZHULONG_PIXEL_FORMAT_RGBA32,
                                                ZHULONG_PIXEL_FORMAT_UNKNOWN}) {
        rc = run_letterbox(*vision_backend, nullptr, 2, 2, 0,
                           unsupported, ZHULONG_PIXEL_FORMAT_RGB24, 2, 2, letterbox_out);
        assert(rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    }
    // Supported formats with nullptr plane fail with INVALID_ARGUMENT.
    for (zhulong_pixel_format_t supported_null : {ZHULONG_PIXEL_FORMAT_NV12,
                                                  ZHULONG_PIXEL_FORMAT_YUV420P}) {
        rc = run_letterbox(*vision_backend, nullptr, 2, 2, 0,
                           supported_null, ZHULONG_PIXEL_FORMAT_RGB24, 2, 2, letterbox_out);
        assert(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT);
    }

    // Test YUV420P letterbox.
    {
        std::vector<uint8_t> y_plane = {100, 100, 100, 100};
        std::vector<uint8_t> u_plane = {128};
        std::vector<uint8_t> v_plane = {128};
        zhulong_frame_view yuv_frame{};
        yuv_frame.width = 2;
        yuv_frame.height = 2;
        yuv_frame.format = ZHULONG_PIXEL_FORMAT_YUV420P;
        yuv_frame.planes[0] = y_plane.data();
        yuv_frame.planes[1] = u_plane.data();
        yuv_frame.planes[2] = v_plane.data();
        yuv_frame.strides[0] = 2;
        yuv_frame.strides[1] = 1;
        yuv_frame.strides[2] = 1;

        zhulong::LetterboxParams yuv_params;
        yuv_params.target_width = 2;
        yuv_params.target_height = 2;
        yuv_params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;
        zhulong::Tensor yuv_out;
        rc = vision_backend->letterbox(yuv_frame, yuv_out, yuv_params);
        assert(rc == ZHULONG_PLUGIN_OK);
        assert(yuv_out.shape() == std::vector<int64_t>({2, 2, 3}));
        const uint8_t* yuv_pixels = yuv_out.data<uint8_t>();
        assert(yuv_pixels != nullptr);
        for (int i = 0; i < 4 * 3; ++i) {
            assert(yuv_pixels[i] == 100);
        }
    }

    // Test NV12 letterbox.
    {
        std::vector<uint8_t> y_plane = {150, 150, 150, 150};
        std::vector<uint8_t> uv_plane = {128, 128};
        zhulong_frame_view nv12_frame{};
        nv12_frame.width = 2;
        nv12_frame.height = 2;
        nv12_frame.format = ZHULONG_PIXEL_FORMAT_NV12;
        nv12_frame.planes[0] = y_plane.data();
        nv12_frame.planes[1] = uv_plane.data();
        nv12_frame.strides[0] = 2;
        nv12_frame.strides[1] = 2;

        zhulong::LetterboxParams nv12_params;
        nv12_params.target_width = 2;
        nv12_params.target_height = 2;
        nv12_params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;
        zhulong::Tensor nv12_out;
        rc = vision_backend->letterbox(nv12_frame, nv12_out, nv12_params);
        assert(rc == ZHULONG_PLUGIN_OK);
        assert(nv12_out.shape() == std::vector<int64_t>({2, 2, 3}));
        const uint8_t* nv12_pixels = nv12_out.data<uint8_t>();
        assert(nv12_pixels != nullptr);
        for (int i = 0; i < 4 * 3; ++i) {
            assert(nv12_pixels[i] == 150);
        }
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
