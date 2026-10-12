#include <algorithm/runtime/runtime_factory.hpp>
#include <algorithm/runtime/vision_backend.hpp>
#include <cassert>
#include <iostream>
#include <vector>

int main() {
    // 1. 测试推理后端
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
    assert(out_data[4] > 0.8f); // confidence check

    // 2. 测试视觉前处理 Letterbox
    auto vision_backend = zhulong::get_default_vision_backend();
    assert(vision_backend != nullptr);

    std::vector<uint8_t> dummy_bgr(1920 * 1080 * 3, 200);
    zhulong_frame_view frame;
    frame.width = 1920;
    frame.height = 1080;
    frame.planes[0] = dummy_bgr.data();
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;

    zhulong::Tensor letterbox_out;
    zhulong::LetterboxParams params;
    params.target_width = 640;
    params.target_height = 640;

    rc = vision_backend->letterbox(frame, letterbox_out, params);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(letterbox_out.shape().size() == 3);
    assert(letterbox_out.shape()[0] == 640 && letterbox_out.shape()[1] == 640 && letterbox_out.shape()[2] == 3);
    assert(params.scale > 0.0f);
    assert(params.pad_top >= 0);

    // 3. 测试 NMS
    std::vector<zhulong_bbox> boxes(3);
    // 两个高度重叠的框
    boxes[0].x = 10; boxes[0].y = 10; boxes[0].width = 100; boxes[0].height = 100; boxes[0].confidence = 0.9f;
    boxes[1].x = 12; boxes[1].y = 12; boxes[1].width = 98;  boxes[1].height = 98;  boxes[1].confidence = 0.8f;
    // 一个独立无重叠的框
    boxes[2].x = 300; boxes[2].y = 300; boxes[2].width = 50; boxes[2].height = 50; boxes[2].confidence = 0.7f;

    auto kept_boxes = zhulong::nms(boxes, 0.45f);
    assert(kept_boxes.size() == 2);
    assert(kept_boxes[0].confidence == 0.9f);
    assert(kept_boxes[1].confidence == 0.7f);

    std::cout << "Algorithm runtime tests passed successfully!" << std::endl;
    return 0;
}
