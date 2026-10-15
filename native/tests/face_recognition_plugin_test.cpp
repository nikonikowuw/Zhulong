#include <zhulong/plugin.h>
#include <algorithm/runtime/gallery_registry.hpp>
#include <dlfcn.h>
#include <cassert>
#include <cmath>
#include <cstring>
#include <iostream>
#include <vector>

static zhulong_status_t load_plugin(const char* so_path, void** out_handle, zhulong_plugin_api_v1* out_api) {
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
    const char* plugin_so = (argc > 1) ? argv[1] : "./libFaceRecognitionPlugin.so";

    void* handle = nullptr;
    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = load_plugin(plugin_so, &handle, &api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::cerr << "Failed to load plugin: " << plugin_so << std::endl;
        return 1;
    }

    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "FaceRecognition") == 0);

    zhulong_model_group_h model = nullptr;
    rc = api.model_create("{\"threshold\":0.40,\"top_k\":3}", &model);
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 100, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    // 构造有效人脸像素（100x100 BGR）
    std::vector<uint8_t> test_pixels(100 * 100 * 3, 0);
    uint64_t pixel_sum = 0;
    for (size_t i = 0; i < test_pixels.size(); ++i) {
        test_pixels[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
        pixel_sum += test_pixels[i];
    }

    zhulong_frame_view frame{};
    frame.frame_id = 1;
    frame.width = 100;
    frame.height = 100;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;
    frame.planes[0] = test_pixels.data();
    frame.strides[0] = 300;

    // =========================================================================
    // 1. 空底库自测（验包场景）
    // =========================================================================
    auto& gallery = zhulong::runtime::GalleryRegistry::instance();
    gallery.clear();
    assert(gallery.size() == 0);

    zhulong_inference_result* res = nullptr;
    rc = api.process(task, &frame, &res);
    assert(rc == ZHULONG_PLUGIN_OK && res != nullptr);
    assert(res->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
    assert(res->count == 1);

    const auto* persons = reinterpret_cast<const zhulong_person_item*>(res->data);
    assert(persons[0].track_id == 0); // 纯人脸退化语义
    assert(persons[0].body_score == 1.0f);
    assert(persons[0].face_count == 1);
    assert(persons[0].faces != nullptr);

    const auto& face0 = persons[0].faces[0];
    assert(face0.box[0] == 0.20f && face0.box[1] == 0.15f);
    assert(face0.landmarks[0][0] == 0.32f && face0.landmarks[0][1] == 0.32f);
    assert(face0.matched == 0);
    assert(face0.similarity == 0.0f);
    assert(std::strlen(face0.person_id) == 0);
    assert(face0.candidate_count == 0);
    api.result_release(res);

    // =========================================================================
    // 2. 命中测试（底库已录入该特征）
    // =========================================================================
    // 计算期望同构特征向量
    std::vector<float> expected_features(512, 0.0f);
    float norm_sq = 0.0f;
    for (uint32_t i = 0; i < 512; ++i) {
        float val = std::sin(static_cast<float>(pixel_sum % 10007 + i * 37 + 1)) * 0.5f +
                    std::cos(static_cast<float>(100 * 31 + 100 * 17 + i)) * 0.5f;
        expected_features[i] = val;
        norm_sq += val * val;
    }
    float norm = std::sqrt(norm_sq);
    for (uint32_t i = 0; i < 512; ++i) {
        expected_features[i] /= norm;
    }

    gallery.upsert({
        "face_test_01",
        "P_USER_999",
        "测试员Alice",
        "alice.jpg",
        expected_features
    });
    assert(gallery.size() == 1);

    res = nullptr;
    rc = api.process(task, &frame, &res);
    assert(rc == ZHULONG_PLUGIN_OK && res != nullptr);
    const auto* persons_hit = reinterpret_cast<const zhulong_person_item*>(res->data);
    assert(persons_hit[0].face_count == 1);
    const auto& face_hit = persons_hit[0].faces[0];
    assert(face_hit.matched == 1);
    assert(face_hit.similarity > 0.999f);
    assert(std::strcmp(face_hit.person_id, "P_USER_999") == 0);
    assert(std::strcmp(face_hit.person_name, "测试员Alice") == 0);
    assert(face_hit.candidate_count == 1);
    assert(std::strcmp(face_hit.candidates[0].person_id, "P_USER_999") == 0);
    api.result_release(res);

    // =========================================================================
    // 3. 拒绝测试（异人不同特征，低于阈值拒绝）
    // =========================================================================
    gallery.clear();
    // 插入一个几乎反向的特征向量
    std::vector<float> diff_features(512, 0.0f);
    for (uint32_t i = 0; i < 512; ++i) {
        diff_features[i] = -expected_features[i];
    }
    gallery.upsert({
        "face_other",
        "P_OTHER",
        "其他人员Bob",
        "bob.jpg",
        diff_features
    });

    res = nullptr;
    rc = api.process(task, &frame, &res);
    assert(rc == ZHULONG_PLUGIN_OK && res != nullptr);
    const auto* persons_diff = reinterpret_cast<const zhulong_person_item*>(res->data);
    const auto& face_diff = persons_diff[0].faces[0];
    assert(face_diff.matched == 0);
    assert(face_diff.similarity < 0.40f);
    assert(std::strlen(face_diff.person_id) == 0);
    api.result_release(res);

    // =========================================================================
    // 4. 全黑图像负样本测试（无脸检测）
    // =========================================================================
    std::vector<uint8_t> black_pixels(100 * 100 * 3, 0);
    zhulong_frame_view black_frame = frame;
    black_frame.planes[0] = black_pixels.data();

    res = nullptr;
    rc = api.process(task, &black_frame, &res);
    assert(rc == ZHULONG_PLUGIN_OK && res != nullptr);
    assert(res->count == 0);
    api.result_release(res);

    // 释放资源
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    dlclose(handle);

    std::cout << "All FaceRecognitionPlugin tests passed successfully!" << std::endl;
    return 0;
}
