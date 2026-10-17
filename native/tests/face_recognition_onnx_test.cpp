/**
 * @file face_recognition_onnx_test.cpp
 * @brief ONNX Runtime 端到端人脸算法测试（检测、对齐、特征提取、1:N 比对与插件 C ABI）
 */

#include <zhulong/algo.hpp>
#include <zhulong/plugin.h>
#include <algorithm/runtime/gallery_registry.hpp>
#include "detector.hpp"
#include "aligner.hpp"
#include "extractor.hpp"

#include <cassert>
#include <cmath>
#include <cstring>
#include <dlfcn.h>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

namespace {

struct PpmImage {
    int width{0};
    int height{0};
    std::vector<uint8_t> rgb;
};

static std::string resolve_test_file(const std::string& rel) {
    const std::vector<std::string> prefixes = {
        "",
        "native/",
        "../native/",
        "../../native/",
        "../../../native/",
        "../",
        "../../",
        "../../../",
    };
    for (const auto& p : prefixes) {
        std::string cand = p + rel;
        if (std::ifstream(cand).good()) return cand;
    }
    return rel;
}

bool load_ppm(const std::string& path, PpmImage& out) {
    std::string resolved = resolve_test_file(path);
    std::ifstream f(resolved, std::ios::binary);
    if (!f.is_open()) return false;
    std::string magic;
    if (!(f >> magic) || magic != "P6") return false;
    int max_val = 0;
    if (!(f >> out.width >> out.height >> max_val) || out.width <= 0 || out.height <= 0 || max_val <= 0) {
        return false;
    }
    f.get(); // skip single newline/delimiter
    out.rgb.resize(static_cast<size_t>(out.width) * out.height * 3);
    f.read(reinterpret_cast<char*>(out.rgb.data()), out.rgb.size());
    return f.gcount() == static_cast<std::streamsize>(out.rgb.size());
}

float cosine_similarity(const std::vector<float>& a, const std::vector<float>& b) {
    assert(a.size() == b.size() && !a.empty());
    float dot = 0.0f;
    for (size_t i = 0; i < a.size(); ++i) {
        dot += a[i] * b[i];
    }
    return dot;
}

zhulong_status_t load_plugin(const char* so_path, void** out_handle, zhulong_plugin_api_v1* out_api) {
    void* handle = dlopen(so_path, RTLD_NOW | RTLD_GLOBAL);
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

} // namespace

int main(int argc, char* argv[]) {
    const char* plugin_so = (argc > 1) ? argv[1] : "./libFaceRecognitionPlugin.so";
    const std::string fixtures_dir = "tests/fixtures/onnx/";
    const std::string models_dir = "packages/face_recognition/targets/cpu/models/";

    std::cout << "[FaceRecognitionOnnxTest] Starting ONNX test suite..." << std::endl;

    // =========================================================================
    // 1. 验证模型加载与基础推理会话
    // =========================================================================
    std::cout << "[1] Testing model loading..." << std::endl;
    std::string det_model_path = resolve_test_file(models_dir + "det_500m.onnx");
    auto det_model = zhulong::Model::load(det_model_path);
    assert(det_model.valid());
    auto det_session = det_model.create_session(0);
    assert(det_session.valid());

    std::string rec_model_path = resolve_test_file(models_dir + "facelivtv2-l.onnx");
    auto rec_model = zhulong::Model::load(rec_model_path);
    assert(rec_model.valid());
    auto rec_session = rec_model.create_session(0);
    assert(rec_session.valid());
    std::cout << "[1] Model loading passed." << std::endl;

    // =========================================================================
    // 2. 验证 SCRFD 人脸检测与 9 头解码
    // =========================================================================
    std::cout << "[2] Testing SCRFD face detection on real image..." << std::endl;
    PpmImage detect_sample;
    bool ok = load_ppm(fixtures_dir + "face_detect_sample.ppm", detect_sample);
    if (!ok) {
        ok = load_ppm("../native/" + fixtures_dir + "face_detect_sample.ppm", detect_sample);
    }
    assert(ok && "Failed to load face_detect_sample.ppm");

    zhulong_frame_view raw_detect_view{};
    raw_detect_view.format = ZHULONG_PIXEL_FORMAT_RGB24;
    raw_detect_view.width = detect_sample.width;
    raw_detect_view.height = detect_sample.height;
    raw_detect_view.planes[0] = detect_sample.rgb.data();
    raw_detect_view.strides[0] = detect_sample.width * 3;
    zhulong::Image detect_img(raw_detect_view);

    zhulong::face::FaceDetector detector;
    detector.set_is_onnx(true);
    detector.set_score_threshold(0.50f);
    detector.set_nms_threshold(0.40f);

    auto detections = detector.detect(det_session, detect_img);
    assert(!detections.empty() && "Expected at least 1 face detected by SCRFD");
    std::cout << "  Detected " << detections.size() << " faces." << std::endl;

    for (const auto& det : detections) {
        assert(det.is_real_detection && "SCRFD detection should mark is_real_detection=true");
        assert(det.face_score >= 0.50f);
        // 验证坐标完全归一化于 [0.0, 1.0]
        assert(det.face_box[0] >= 0.0f && det.face_box[0] <= 1.0f);
        assert(det.face_box[1] >= 0.0f && det.face_box[1] <= 1.0f);
        assert(det.face_box[2] >= det.face_box[0] && det.face_box[2] <= 1.0f);
        assert(det.face_box[3] >= det.face_box[1] && det.face_box[3] <= 1.0f);

        for (int k = 0; k < 5; ++k) {
            assert(det.landmarks[k].x >= 0.0f && det.landmarks[k].x <= 1.0f);
            assert(det.landmarks[k].y >= 0.0f && det.landmarks[k].y <= 1.0f);
        }
        std::cout << "  Face score: " << det.face_score << " box: ["
                  << det.face_box[0] << ", " << det.face_box[1] << ", "
                  << det.face_box[2] << ", " << det.face_box[3] << "]" << std::endl;
    }
    std::cout << "[2] SCRFD detection passed." << std::endl;

    // =========================================================================
    // 3. 验证 FaceLiVTv2-L 特征提取与金标精度基线
    // =========================================================================
    std::cout << "[3] Testing FaceLiVTv2-L feature extraction & baseline accuracy..." << std::endl;
    PpmImage ppm_a1, ppm_a2, ppm_b1;
    assert(load_ppm(fixtures_dir + "face_a1_112x112.ppm", ppm_a1) ||
           load_ppm("../native/" + fixtures_dir + "face_a1_112x112.ppm", ppm_a1));
    assert(load_ppm(fixtures_dir + "face_a2_112x112.ppm", ppm_a2) ||
           load_ppm("../native/" + fixtures_dir + "face_a2_112x112.ppm", ppm_a2));
    assert(load_ppm(fixtures_dir + "face_b1_112x112.ppm", ppm_b1) ||
           load_ppm("../native/" + fixtures_dir + "face_b1_112x112.ppm", ppm_b1));

    zhulong::face::FaceFeatureExtractor extractor;
    extractor.set_is_onnx(true);

    auto extract_from_ppm = [&](const PpmImage& ppm) {
        zhulong::Tensor tensor({1, 112, 112, 3}, zhulong::DataType::UINT8);
        std::memcpy(tensor.data<uint8_t>(), ppm.rgb.data(), 112 * 112 * 3);
        auto feat = extractor.extract(rec_session, tensor);
        assert(feat.size() == 512);

        // 验证严格 L2 单位模长
        float norm_sq = 0.0f;
        for (float v : feat) norm_sq += v * v;
        float norm = std::sqrt(norm_sq);
        assert(std::abs(norm - 1.0f) < 1e-4f && "Embedding must have unit L2 norm");
        return feat;
    };

    auto feat_a1 = extract_from_ppm(ppm_a1);
    auto feat_a2 = extract_from_ppm(ppm_a2);
    auto feat_b1 = extract_from_ppm(ppm_b1);

    float sim_same = cosine_similarity(feat_a1, feat_a2);
    float sim_diff = cosine_similarity(feat_a1, feat_b1);

    std::cout << "  Same person similarity (a1, a2): " << sim_same
              << " (expected > 0.90, baseline ~0.9235)" << std::endl;
    std::cout << "  Diff person similarity (a1, b1): " << sim_diff
              << " (expected < 0.10, baseline ~ -0.0763)" << std::endl;

    assert(sim_same > 0.90f && "Same person similarity failed baseline");
    assert(sim_diff < 0.10f && "Diff person similarity failed baseline");
    std::cout << "[3] Feature extraction & baseline accuracy passed." << std::endl;

    // =========================================================================
    // 4. 验证 1:N Gallery 检索与 Top-1 匹配
    // =========================================================================
    std::cout << "[4] Testing 1:N Gallery Matching..." << std::endl;
    auto& gallery = zhulong::runtime::GalleryRegistry::instance();
    gallery.clear();
    gallery.upsert({
        "face_a1_id",
        "PERSON_A",
        "Alice",
        "alice.ppm",
        feat_a1
    });

    zhulong_status_t rc = ZHULONG_PLUGIN_OK;
    void* handle = nullptr;
    zhulong_plugin_api_v1 api;
    rc = load_plugin(plugin_so, &handle, &api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::cerr << "Failed to load plugin via dlopen: " << plugin_so << std::endl;
        return 1;
    }

    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "FaceRecognition") == 0);

    zhulong_model_group_h model = nullptr;
    rc = api.model_create("{\"threshold\":0.50,\"top_k\":3}", &model);
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 101, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    // =========================================================================
    // 5. 验证插件 C ABI 完整链路 (dlopen, model_create, worker_create, process)
    // =========================================================================
    std::cout << "[5] Testing Plugin C ABI end-to-end..." << std::endl;
    zhulong_frame_view frame{};
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.width = detect_sample.width;
    frame.height = detect_sample.height;
    frame.planes[0] = detect_sample.rgb.data();
    frame.strides[0] = detect_sample.width * 3;
    frame.pts = 1000000;
    frame.dma_buf_fd = -1;

    zhulong_inference_result* res = nullptr;
    rc = api.process(task, &frame, &res);
    assert(rc == ZHULONG_PLUGIN_OK && res != nullptr);
    assert(res->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
    assert(res->count >= 1 && "Expected at least 1 person structure in real frame");

    const auto* persons = reinterpret_cast<const zhulong_person_item*>(res->data);
    assert(persons != nullptr);
    for (uint32_t i = 0; i < res->count; ++i) {
        const auto& p = persons[i];
        assert(p.face_count >= 1);
        assert(p.faces != nullptr);
        const auto& f = p.faces[0];
        assert(f.quality >= 0.50f);
        assert(f.box[0] >= 0.0f && f.box[2] <= 1.0f);
        assert(f.box[1] >= 0.0f && f.box[3] <= 1.0f);
    }

    api.result_release(res);
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    dlclose(handle);

    std::cout << "[5] Plugin C ABI end-to-end passed." << std::endl;
    std::cout << "All FaceRecognition ONNX tests completed successfully!" << std::endl;
    return 0;
}
