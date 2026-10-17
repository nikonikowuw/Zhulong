/**
 * @file face_recognition.cpp
 * @brief 人脸人体结构化算法插件主入口 (纯 C ABI 导出)
 */

#include "detector.hpp"
#include "aligner.hpp"
#include "extractor.hpp"
#include "matcher.hpp"
#include <zhulong/algo.hpp>
#include <dlfcn.h>
#include <cmath>
#include <cstdint>
#include <fstream>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::face {

static std::string resolve_model_path(const std::string& rel_path) {
    if (std::ifstream(rel_path).good()) {
        return rel_path;
    }
#if defined(__arm__) || defined(__aarch64__)
    const std::vector<std::string> target_dirs = {
        "targets/rk3568/",
        "targets/cpu/"
    };
#else
    const std::vector<std::string> target_dirs = {
        "targets/cpu/",
        "targets/rk3568/"
    };
#endif

    std::vector<std::string> cwd_candidates;
    for (const auto& tdir : target_dirs) {
        cwd_candidates.push_back("packages/face_recognition/" + tdir + rel_path);
        cwd_candidates.push_back("../../../packages/face_recognition/" + tdir + rel_path);
        cwd_candidates.push_back("../../packages/face_recognition/" + tdir + rel_path);
        cwd_candidates.push_back("../packages/face_recognition/" + tdir + rel_path);
    }

    for (const auto& cand : cwd_candidates) {
        if (std::ifstream(cand).good()) {
            return cand;
        }
    }
    const char* env_dir = std::getenv("ZHULONG_FACE_MODEL_DIR");
    if (!env_dir) env_dir = std::getenv("ZHULONG_MODEL_DIR");
    if (env_dir && env_dir[0] != '\0') {
        std::string cand = std::string(env_dir) + "/" + rel_path;
        if (std::ifstream(cand).good()) {
            return cand;
        }
    }
    Dl_info info;
    if (dladdr(reinterpret_cast<void*>(&resolve_model_path), &info) && info.dli_fname) {
        std::string so_path = info.dli_fname;
        auto pos = so_path.find_last_of('/');
        if (pos != std::string::npos) {
            std::string dir = so_path.substr(0, pos);
            std::vector<std::string> so_candidates = { dir + "/" + rel_path };
            for (const auto& tdir : target_dirs) {
                so_candidates.push_back(dir + "/../../../packages/face_recognition/" + tdir + rel_path);
                so_candidates.push_back(dir + "/../../packages/face_recognition/" + tdir + rel_path);
                so_candidates.push_back(dir + "/../packages/face_recognition/" + tdir + rel_path);
            }
            for (const auto& cand : so_candidates) {
                if (std::ifstream(cand).good()) {
                    return cand;
                }
            }
        }
    }
    return rel_path;
}

// 模型扩展名按目标架构择优：ARM 板端优先 RKNN，其余平台优先 ONNX；两者都缺失时回退历史 .weights 桩。
#if defined(__arm__) || defined(__aarch64__)
constexpr const char* PREFERRED_MODEL_EXT = ".rknn";
constexpr const char* FALLBACK_MODEL_EXT = ".onnx";
#else
constexpr const char* PREFERRED_MODEL_EXT = ".onnx";
constexpr const char* FALLBACK_MODEL_EXT = ".rknn";
#endif

/**
 * @brief 按「首选扩展名 → 回退扩展名 → .weights 桩」顺序加载模型
 *
 * 检测与特征提取两条流水线共享同一套寻址与择优逻辑，避免两处实现漂移。
 *
 * @param base_name 不含扩展名的模型相对路径（如 "models/det_500m"）
 * @param[out] is_onnx 命中的模型是否为 ONNX 格式（决定预处理张量布局）
 * @return 加载成功的模型实例；全部候选失败时返回 valid() == false 的空模型
 */
static zhulong::Model load_model_preferred(const char* base_name, bool& is_onnx) {
    const std::string base(base_name);
    const char* candidates[] = {PREFERRED_MODEL_EXT, FALLBACK_MODEL_EXT, ".weights"};
    for (const char* ext : candidates) {
        zhulong::Model model = zhulong::Model::load(resolve_model_path(base + ext));
        if (model.valid()) {
            is_onnx = (std::string(ext) == ".onnx");
            return model;
        }
    }
    is_onnx = false;
    return zhulong::Model();
}

class FaceRecognitionPlugin {
public:
    FaceRecognitionPlugin() = default;

    zhulong_status_t init(const std::string& config_json) {
        parse_config(config_json);

        // 1. 加载人脸检测模型 (SCRFD det_500m)
        bool det_is_onnx = false;
        det_model_ = load_model_preferred("models/det_500m", det_is_onnx);

        // 2. 加载人脸特征提取模型 (FaceLiVTv2-L)
        bool rec_is_onnx = false;
        rec_model_ = load_model_preferred("models/facelivtv2-l", rec_is_onnx);

        if (!det_model_.valid() || !rec_model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        detector_.set_is_onnx(det_is_onnx);
        extractor_.set_is_onnx(rec_is_onnx);

        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        auto det_session = det_model_.create_session(worker_id);
        auto rec_session = rec_model_.create_session(worker_id);
        {
            std::lock_guard<std::mutex> lock(sessions_mu_);
            rec_sessions_[worker_id] = std::move(rec_session);
        }
        return det_session;
    }

    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image) {
        uint32_t worker_id = session.worker_id();
        zhulong::Session* rec_session = nullptr;
        {
            std::lock_guard<std::mutex> lock(sessions_mu_);
            auto it = rec_sessions_.find(worker_id);
            if (it != rec_sessions_.end()) {
                rec_session = &it->second;
            }
        }

        // 1. 真实目标检测与 5 点关键点定位 (SCRFD on RKNN / CPU fallback)
        auto detections = detector_.detect(session, image);
        if (detections.empty()) {
            return {};
        }

        std::vector<zhulong::PersonItem> results;
        results.reserve(detections.size());

        auto gallery_snapshot = zhulong::GalleryRegistry::instance().snapshot();

        for (auto& det : detections) {
            // 2. 优先使用原帧高清像素坐标反算关键点进行仿射对齐
            std::array<Point2f, 5> src_pixels;
            if (det.landmarks_pixels[0].x > 0.0f || det.landmarks_pixels[0].y > 0.0f) {
                src_pixels = det.landmarks_pixels;
            } else {
                for (size_t i = 0; i < 5; ++i) {
                    src_pixels[i].x = det.landmarks[i].x * static_cast<float>(image.width());
                    src_pixels[i].y = det.landmarks[i].y * static_cast<float>(image.height());
                }
            }

            // 3. Umeyama 仿射对齐与 112x112 重采样 (单阶段直写特征输入 Tensor)
            std::array<float, 6> affine_mat{};
            std::vector<float> probe_features;

            if (Aligner::estimate_similarity_transform(src_pixels, affine_mat)) {
                zhulong::Tensor rec_input({1, 112, 112, 3}, zhulong::DataType::UINT8);
                if (Aligner::crop_and_align_direct(image, affine_mat, rec_input.data<uint8_t>(), 112, 112)) {
                    if (det.is_real_detection && rec_session && rec_session->valid()) {
                        probe_features = extractor_.extract(*rec_session, rec_input);
                    } else {
                        probe_features = extractor_.extract(rec_input.data<uint8_t>(), 112, 112, det.face_box);
                    }
                }
            }

            if (probe_features.size() != 512) {
                probe_features.assign(512, 0.0f);
            }

            // 4. 1:N 底库比对与 Top-K 检索
            MatchResult match_res = matcher_.match(probe_features.data(), 512, *gallery_snapshot);

            // 5. 构造人脸项
            zhulong::FaceItem face;
            face.box[0] = det.face_box[0];
            face.box[1] = det.face_box[1];
            face.box[2] = det.face_box[2];
            face.box[3] = det.face_box[3];
            face.quality = det.face_score;
            face.landmarks = {
                {det.landmarks[0].x, det.landmarks[0].y},
                {det.landmarks[1].x, det.landmarks[1].y},
                {det.landmarks[2].x, det.landmarks[2].y},
                {det.landmarks[3].x, det.landmarks[3].y},
                {det.landmarks[4].x, det.landmarks[4].y}
            };
            face.matched = match_res.matched;
            face.similarity = match_res.similarity;
            face.person_id = match_res.person_id;
            face.person_name = match_res.person_name;
            face.matched_face_id = match_res.matched_face_id;
            face.candidates = std::move(match_res.candidates);
            face.is_fused = false;

            // 6. 构造人脸关联人体项 (PersonItem)
            zhulong::PersonItem person;
            person.track_id = det.track_id;
            person.body_box[0] = det.body_box[0];
            person.body_box[1] = det.body_box[1];
            person.body_box[2] = det.body_box[2];
            person.body_box[3] = det.body_box[3];
            person.body_score = det.body_score;
            person.faces.push_back(std::move(face));

            results.push_back(std::move(person));
        }

        return results;
    }

private:
    void parse_config(const std::string& json) {
        auto pos_th = json.find("\"threshold\"");
        if (pos_th != std::string::npos) {
            auto colon = json.find(':', pos_th);
            if (colon != std::string::npos) {
                try {
                    float th = std::stof(json.substr(colon + 1));
                    if (th >= 0.0f && th <= 1.0f) {
                        matcher_.set_threshold(th);
                    }
                } catch (...) {}
            }
        }
    }

    zhulong::Model det_model_;
    zhulong::Model rec_model_;
    std::mutex sessions_mu_;
    std::unordered_map<uint32_t, zhulong::Session> rec_sessions_;

    FaceDetector detector_;
    Aligner aligner_;
    FaceFeatureExtractor extractor_;
    Matcher matcher_{0.40f, 3};
};

/**
 * @brief 宿主直连的单次人脸特征提取上下文
 *
 * 由 `zhulong_face_extract_feature` 使用：懒加载检测/特征模型并缓存会话，
 * 由宿主的 TTL Reaper 通过 `zhulong_face_extract_release` 显式释放。
 * 所有公开方法内部串行化，可在任意线程并发调用。
 */
class FaceExtractorContext {
public:
    static FaceExtractorContext& instance() {
        static FaceExtractorContext inst;
        return inst;
    }

    /** @brief 释放模型会话与权重（宿主 TTL 空闲回收 / dlclose 前必须调用） */
    void release() {
        std::lock_guard<std::mutex> lock(mu_);
        if (!initialized_) return;
        det_session_ = zhulong::Session();
        rec_session_ = zhulong::Session();
        det_model_ = zhulong::Model();
        rec_model_ = zhulong::Model();
        initialized_ = false;
    }

    zhulong_status_t extract(
        const uint8_t* pixel_data,
        int32_t width,
        int32_t height,
        int32_t stride,
        int32_t format,
        float* out_features,
        uint32_t* feature_dim
    ) {
        if (!pixel_data || width <= 0 || height <= 0 || !out_features || !feature_dim || *feature_dim < 512) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }
        if (format != ZHULONG_PIXEL_FORMAT_BGR24 && format != ZHULONG_PIXEL_FORMAT_RGB24) {
            return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        }

        const int64_t min_stride = static_cast<int64_t>(width) * 3;
        int32_t effective_stride = (stride == 0) ? static_cast<int32_t>(min_stride) : stride;
        if (effective_stride < min_stride) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        std::lock_guard<std::mutex> lock(mu_);
        zhulong_status_t st = init_locked();
        if (st != ZHULONG_PLUGIN_OK) {
            return st;
        }

        // 色彩通道处理：若是 BGR24，统一转换为 RGB24 供 SCRFD 与特征提取
        std::vector<uint8_t> rgb_converted;
        const uint8_t* effective_pixels = pixel_data;
        if (format == ZHULONG_PIXEL_FORMAT_BGR24) {
            rgb_converted.resize(static_cast<size_t>(effective_stride) * height);
            for (int y = 0; y < height; ++y) {
                const uint8_t* src_row = pixel_data + static_cast<size_t>(y) * effective_stride;
                uint8_t* dst_row = rgb_converted.data() + static_cast<size_t>(y) * effective_stride;
                for (int x = 0; x < width; ++x) {
                    dst_row[x * 3 + 0] = src_row[x * 3 + 2]; // R
                    dst_row[x * 3 + 1] = src_row[x * 3 + 1]; // G
                    dst_row[x * 3 + 2] = src_row[x * 3 + 0]; // B
                }
            }
            effective_pixels = rgb_converted.data();
        }

        zhulong_frame_view view{};
        view.width = width;
        view.height = height;
        view.format = ZHULONG_PIXEL_FORMAT_RGB24;
        view.planes[0] = effective_pixels;
        view.strides[0] = effective_stride;
        view.dma_buf_fd = -1;

        zhulong::Image image(view);

        // 1. 人脸检测与 5 点关键点定位
        auto detections = detector_.detect(det_session_, image);
        if (detections.empty()) {
            return ZHULONG_PLUGIN_ERR_NOT_FOUND;
        }

        // 2. 选取置信度最高的主人脸
        const FaceDetection* best_det = &detections[0];
        for (size_t i = 1; i < detections.size(); ++i) {
            if (detections[i].face_score > best_det->face_score) {
                best_det = &detections[i];
            }
        }

        // 检出结果来自 CPU 降级桩而非真实神经网络时，说明当前推理后端不具备真实人脸能力：
        // 必须显式返回 NOT_SUPPORTED（闭环报错），严禁用桩坐标伪造出「看起来有效」的特征向量。
        if (!best_det->is_real_detection) {
            return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        }
        if (best_det->face_score < 0.50f) {
            return ZHULONG_PLUGIN_ERR_NOT_FOUND;
        }

        // 3. 关键点仿射对齐
        std::array<Point2f, 5> src_pixels;
        if (best_det->landmarks_pixels[0].x > 0.0f || best_det->landmarks_pixels[0].y > 0.0f) {
            src_pixels = best_det->landmarks_pixels;
        } else {
            for (size_t i = 0; i < 5; ++i) {
                src_pixels[i].x = best_det->landmarks[i].x * static_cast<float>(width);
                src_pixels[i].y = best_det->landmarks[i].y * static_cast<float>(height);
            }
        }

        std::array<float, 6> affine_mat{};
        std::vector<float> probe_features;
        if (!Aligner::estimate_similarity_transform(src_pixels, affine_mat)) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        zhulong::Tensor rec_input({1, 112, 112, 3}, zhulong::DataType::UINT8);
        if (!Aligner::crop_and_align_direct(image, affine_mat, rec_input.data<uint8_t>(), 112, 112)) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        probe_features = extractor_.extract(rec_session_, rec_input);

        if (probe_features.size() != 512) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        // 验证 L2 归一化 (防除零)
        float norm_sq = 0.0f;
        for (float v : probe_features) norm_sq += v * v;
        float norm = std::sqrt(norm_sq);
        if (norm < 1e-6f) norm = 1.0f;
        for (size_t i = 0; i < 512; ++i) {
            out_features[i] = probe_features[i] / norm;
        }
        *feature_dim = 512;

        return ZHULONG_PLUGIN_OK;
    }

private:
    /** @brief 懒加载检测与特征模型（调用方必须已持有 mu_） */
    zhulong_status_t init_locked() {
        if (initialized_) {
            return ZHULONG_PLUGIN_OK;
        }

        // 1. 加载人脸检测模型 (SCRFD det_500m)
        bool det_is_onnx = false;
        det_model_ = load_model_preferred("models/det_500m", det_is_onnx);

        // 2. 加载人脸特征提取模型 (FaceLiVTv2-L)
        bool rec_is_onnx = false;
        rec_model_ = load_model_preferred("models/facelivtv2-l", rec_is_onnx);

        // 模型缺失属于「能力不可用」，绝不等同于「图中没有人脸」：必须闭环报错，禁止伪造特征。
        if (!det_model_.valid() || !rec_model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        detector_.set_is_onnx(det_is_onnx);
        extractor_.set_is_onnx(rec_is_onnx);

        det_session_ = det_model_.create_session(999);
        rec_session_ = rec_model_.create_session(999);

        if (!det_session_.valid() || !rec_session_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        initialized_ = true;
        return ZHULONG_PLUGIN_OK;
    }

    std::mutex mu_;
    bool initialized_{false};
    zhulong::Model det_model_;
    zhulong::Model rec_model_;
    zhulong::Session det_session_;
    zhulong::Session rec_session_;
    FaceDetector detector_;
    Aligner aligner_;
    FaceFeatureExtractor extractor_;
};

} // namespace zhulong::face

extern "C" {

ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_face_extract_feature(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
) {
    try {
        return zhulong::face::FaceExtractorContext::instance().extract(
            pixel_data, width, height, stride, format, out_features, feature_dim
        );
    } catch (...) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

ZHULONG_PLUGIN_EXPORT void zhulong_face_extract_release() {
    try {
        zhulong::face::FaceExtractorContext::instance().release();
    } catch (...) {}
}

} // extern "C"

// 注册为任务类型 3 (person_structure) 算法插件
ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(
    zhulong::face::FaceRecognitionPlugin,
    "FaceRecognition",
    "1.0.0"
)
