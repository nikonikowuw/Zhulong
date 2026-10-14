#include <zhulong/plugin.h>
#include <dlfcn.h>
#include <cassert>
#include <iostream>
#include <vector>
#include <thread>
#include <cstring>

// 辅助加载函数
static zhulong_status_t load_plugin_table(const char* so_path, void** out_handle, zhulong_plugin_api_v1* out_api) {
    void* handle = dlopen(so_path, RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        std::cerr << "dlopen failed: " << dlerror() << std::endl;
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }
    *out_handle = handle;

    auto get_api = reinterpret_cast<zhulong_status_t (*)(uint32_t, void*, size_t)>(
        dlsym(handle, ZHULONG_PLUGIN_ENTRY_POINT)
    );
    if (!get_api) {
        dlclose(handle);
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    std::memset(out_api, 0, sizeof(*out_api));
    zhulong_status_t rc = get_api(1, out_api, sizeof(*out_api));
    if (rc != ZHULONG_PLUGIN_OK) {
        dlclose(handle);
        return rc;
    }
    return ZHULONG_PLUGIN_OK;
}

int main(int argc, char* argv[]) {
    const char* detector_so = (argc > 1) ? argv[1] : "./libMockDetector.so";
    const char* classifier_so = (argc > 2) ? argv[2] : "./libMockClassifier.so";
    const char* person_structure_so = (argc > 3) ? argv[3] : "./libMockPersonStructure.so";

    // =========================================================================
    // 1. 验证目标检测插件 (MockDetector)
    // =========================================================================
    void* handle1 = nullptr;
    zhulong_plugin_api_v1 api1;
    zhulong_status_t rc = load_plugin_table(detector_so, &handle1, &api1);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(api1.api_version == 1);
    assert(std::strcmp(api1.plugin_name, "MockDetector") == 0);

    zhulong_model_group_h model1 = nullptr;
    rc = api1.model_create("{}", &model1);
    assert(rc == ZHULONG_PLUGIN_OK && model1);

    // 验证多 Worker 并发独立执行上下文
    zhulong_worker_ctx_h worker1 = nullptr;
    zhulong_worker_ctx_h worker2 = nullptr;
    rc = api1.worker_create(model1, 0, &worker1);
    assert(rc == ZHULONG_PLUGIN_OK && worker1);
    rc = api1.worker_create(model1, 1, &worker2);
    assert(rc == ZHULONG_PLUGIN_OK && worker2);

    zhulong_task_ctx_h task1 = nullptr;
    zhulong_task_ctx_h task2 = nullptr;
    rc = api1.task_open(worker1, 1001, &task1);
    assert(rc == ZHULONG_PLUGIN_OK && task1);
    rc = api1.task_open(worker2, 1002, &task2);
    assert(rc == ZHULONG_PLUGIN_OK && task2);

    // 构造测试帧
    std::vector<uint8_t> dummy_bgr(640 * 480 * 3, 100);
    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 42;
    frame.width = 640;
    frame.height = 480;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;
    frame.planes[0] = dummy_bgr.data();

    // 两个线程并发执行推理
    zhulong_inference_result* res1 = nullptr;
    zhulong_inference_result* res2 = nullptr;

    std::thread th1([&]() {
        zhulong_status_t r = api1.process(task1, &frame, &res1);
        assert(r == ZHULONG_PLUGIN_OK);
    });
    std::thread th2([&]() {
        zhulong_status_t r = api1.process(task2, &frame, &res2);
        assert(r == ZHULONG_PLUGIN_OK);
    });

    th1.join();
    th2.join();

    assert(res1 != nullptr && res1->count > 0);
    assert(res2 != nullptr && res2->count > 0);
    assert(res1->result_type == ZHULONG_RESULT_TYPE_DETECTION);
    assert(res2->result_type == ZHULONG_RESULT_TYPE_DETECTION);

    zhulong_bbox* b1 = static_cast<zhulong_bbox*>(res1->data);
    assert(std::strcmp(b1[0].label, "person") == 0);
    assert(b1[0].box[0] == 0.15f);
    assert(b1[0].box[1] == 0.20f);
    assert(b1[0].box[2] == 0.35f);
    assert(b1[0].box[3] == 0.40f);
    assert(b1[0].confidence == 0.92f);
    assert(b1[0].class_id == 1);

    api1.result_release(res1);
    api1.result_release(res2);
    api1.task_close(task1);
    api1.task_close(task2);
    api1.worker_destroy(worker1);
    api1.worker_destroy(worker2);
    api1.model_destroy(model1);
    dlclose(handle1);

    // =========================================================================
    // 2. 验证图像分类插件 (MockClassifier)
    // =========================================================================
    void* handle2 = nullptr;
    zhulong_plugin_api_v1 api2;
    rc = load_plugin_table(classifier_so, &handle2, &api2);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(api2.api_version == 1);
    assert(std::strcmp(api2.plugin_name, "MockClassifier") == 0);

    zhulong_model_group_h model2 = nullptr;
    rc = api2.model_create("{}", &model2);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_worker_ctx_h worker_c = nullptr;
    rc = api2.worker_create(model2, 0, &worker_c);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_task_ctx_h task_c = nullptr;
    rc = api2.task_open(worker_c, 2001, &task_c);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_inference_result* res_c = nullptr;
    rc = api2.process(task_c, &frame, &res_c);
    assert(rc == ZHULONG_PLUGIN_OK && res_c != nullptr);
    assert(res_c->count == 1);
    assert(res_c->result_type == ZHULONG_RESULT_TYPE_CLASSIFICATION);

    auto* classification = static_cast<zhulong_classification_item*>(res_c->data);
    assert(classification[0].class_id == 7);
    assert(classification[0].confidence == 0.98f);
    assert(std::strcmp(classification[0].label, "helmet_detected") == 0);

    api2.result_release(res_c);
    api2.task_close(task_c);
    api2.worker_destroy(worker_c);
    api2.model_destroy(model2);
    dlclose(handle2);

    // =========================================================================
    // 3. 验证结构化人体人脸插件 (MockPersonStructure)
    // =========================================================================
    void* handle3 = nullptr;
    zhulong_plugin_api_v1 api3;
    rc = load_plugin_table(person_structure_so, &handle3, &api3);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(api3.api_version == 1);
    assert(std::strcmp(api3.plugin_name, "MockPersonStructure") == 0);

    zhulong_model_group_h model3 = nullptr;
    rc = api3.model_create("{}", &model3);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_worker_ctx_h worker_p = nullptr;
    rc = api3.worker_create(model3, 0, &worker_p);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_task_ctx_h task_p = nullptr;
    rc = api3.task_open(worker_p, 3001, &task_p);
    assert(rc == ZHULONG_PLUGIN_OK);

    zhulong_inference_result* res_p = nullptr;
    rc = api3.process(task_p, &frame, &res_p);
    assert(rc == ZHULONG_PLUGIN_OK && res_p != nullptr);
    assert(res_p->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
    assert(res_p->count == 1);

    auto* persons = static_cast<zhulong_person_item*>(res_p->data);
    assert(persons[0].track_id == 1001);
    assert(persons[0].body_box[0] == 0.10f);
    assert(persons[0].body_box[1] == 0.15f);
    assert(persons[0].body_box[2] == 0.50f);
    assert(persons[0].body_box[3] == 0.85f);
    assert(persons[0].body_score == 0.95f);
    assert(persons[0].face_count == 1);
    assert(persons[0].faces != nullptr);

    const auto& face = persons[0].faces[0];
    assert(face.box[0] == 0.15f);
    assert(face.box[1] == 0.20f);
    assert(face.box[2] == 0.35f);
    assert(face.box[3] == 0.40f);
    assert(face.quality == 0.88f);
    assert(face.matched == 1);
    assert(face.similarity == 0.92f);
    assert(face.is_fused == 0);
    assert(std::strcmp(face.person_id, "user_007") == 0);
    assert(std::strcmp(face.person_name, "Alice") == 0);

    // 验证 5 点 landmarks
    assert(face.landmarks[0][0] == 0.20f && face.landmarks[0][1] == 0.25f);
    assert(face.landmarks[1][0] == 0.30f && face.landmarks[1][1] == 0.25f);
    assert(face.landmarks[2][0] == 0.25f && face.landmarks[2][1] == 0.30f);
    assert(face.landmarks[3][0] == 0.22f && face.landmarks[3][1] == 0.35f);
    assert(face.landmarks[4][0] == 0.28f && face.landmarks[4][1] == 0.35f);

    // 验证 Top-K candidates
    assert(face.candidate_count == 2);
    assert(face.candidates != nullptr);
    assert(std::strcmp(face.candidates[0].person_id, "user_007") == 0);
    assert(std::strcmp(face.candidates[0].person_name, "Alice") == 0);
    assert(face.candidates[0].similarity == 0.92f);
    assert(std::strcmp(face.candidates[1].person_id, "user_008") == 0);
    assert(std::strcmp(face.candidates[1].person_name, "Bob") == 0);
    assert(face.candidates[1].similarity == 0.45f);

    // 释放单块 Arena
    api3.result_release(res_p);
    api3.task_close(task_p);
    api3.worker_destroy(worker_p);
    api3.model_destroy(model3);
    dlclose(handle3);

    std::cout << "All algorithm runner and sample plugin integration tests passed!" << std::endl;
    return 0;
}
