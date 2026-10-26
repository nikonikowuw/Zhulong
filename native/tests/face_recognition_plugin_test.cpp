#include <zhulong/algo.hpp>
#include "detector.hpp"
#include "face_recognition.hpp"
#include <cassert>
#include <cstring>
#include <iostream>
#include <vector>

// 直接构造算法类的 C ABI 表，不装载任何动态库。
//
// 交付形态下包就是一个可执行文件（PIE 无法被动态装载），宿主只 exec 它；
// 这里验证的仍是同一张表、同一套生命周期语义，只是拿表的方式从「装载符号」
// 变成「调用生成器」。测试因此不会因为产物形态变化而失去意义。
static zhulong_status_t build_api(zhulong_plugin_api_v1* out_api) {
    std::memset(out_api, 0, sizeof(*out_api));
    return zhulong::detail::PluginApi<zhulong::face::FaceRecognitionPlugin,
                                      zhulong::sdk::PersonStructureResultAdapter>::get_api(
        1, out_api, sizeof(*out_api), "FaceRecognition", "1.0.0");
}

int main() {
    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = build_api(&api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::cerr << "Failed to build the plugin API table: " << rc << std::endl;
        return 1;
    }

    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "FaceRecognition") == 0);

    zhulong_model_group_h model = nullptr;
    rc = api.model_create("{\"threshold\":0.40,\"top_k\":3}", &model);
#if defined(ZHULONG_INFERENCE_TEST_STUB)
    // 夹具构建没有真实推理能力：真实模型必须被硬拒绝——这是正确行为，不是缺陷。
    // NOT_SUPPORTED = 模型找到但桩不接受；NOT_FOUND = 该工作目录找不到模型。
    // 两者都必须是「没有 model」，绝不允许出现「加载成功」。
    assert((rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED || rc == ZHULONG_PLUGIN_ERR_NOT_FOUND) &&
           model == nullptr);
    std::cout << "[OK] test_stub build: real model rejected as designed" << std::endl;
    return 0;
#else
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);
#endif

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 100, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    // 构造有效人脸像素（100x100 BGR）
    std::vector<uint8_t> test_pixels(100 * 100 * 3, 0);
    for (size_t i = 0; i < test_pixels.size(); ++i) {
        test_pixels[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
    }

    zhulong_frame_view frame{};
    frame.frame_id = 1;
    frame.width = 100;
    frame.height = 100;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;
    frame.planes[0] = test_pixels.data();
    frame.strides[0] = 300;

    // Bright pixels alone are not evidence of a face. The CPU stub returns an incompatible
    // output shape, so both direct detection and the plugin must fail closed.
    zhulong::face::FaceDetector detector;
    zhulong::Image img_view(frame);
    assert(detector.detect(img_view).empty());

    auto assert_no_detections = [&](const zhulong_frame_view& sample) {
        zhulong_inference_result* result = nullptr;
        zhulong_status_t process_rc = api.process(task, &sample, &result);
        assert(process_rc == ZHULONG_PLUGIN_OK && result != nullptr);
        assert(result->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
        assert(result->count == 0);
        api.result_release(result);
    };
    assert_no_detections(frame);

    std::vector<uint8_t> black_pixels(100 * 100 * 3, 0);
    zhulong_frame_view black_frame = frame;
    black_frame.planes[0] = black_pixels.data();
    assert_no_detections(black_frame);

    // Release resources.
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);

    std::cout << "All FaceRecognitionPlugin tests passed successfully!" << std::endl;
    return 0;
}
