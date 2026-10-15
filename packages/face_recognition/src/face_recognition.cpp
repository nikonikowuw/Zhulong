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
#include <iostream>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::face {

static std::string resolve_model_path(const std::string& rel_path) {
    if (std::ifstream(rel_path).good()) {
        return rel_path;
    }
    Dl_info info;
    if (dladdr(reinterpret_cast<void*>(&resolve_model_path), &info) && info.dli_fname) {
        std::string so_path = info.dli_fname;
        auto pos = so_path.find_last_of('/');
        if (pos != std::string::npos) {
            std::string dir = so_path.substr(0, pos);
            std::string candidate = dir + "/" + rel_path;
            if (std::ifstream(candidate).good()) {
                return candidate;
            }
        }
    }
    return rel_path;
}

class FaceRecognitionPlugin {
public:
    FaceRecognitionPlugin() = default;

    zhulong_status_t init(const std::string& config_json) {
        parse_config(config_json);

        // 1. 加载人脸检测模型 (SCRFD det_500m)
        std::string det_path = resolve_model_path("models/det_500m.rknn");
        det_model_ = zhulong::Model::load(det_path);
        if (!det_model_.valid()) {
            det_model_ = zhulong::Model::load("models/det_500m.onnx");
        }
        if (!det_model_.valid()) {
            det_model_ = zhulong::Model::load("det_500m.weights");
        }

        // 2. 加载人脸特征提取模型 (FaceLiVTv2-L)
        std::string rec_path = resolve_model_path("models/facelivtv2-l.rknn");
        rec_model_ = zhulong::Model::load(rec_path);
        if (!rec_model_.valid()) {
            rec_model_ = zhulong::Model::load("models/facelivtv2-l.onnx");
        }
        if (!rec_model_.valid()) {
            rec_model_ = zhulong::Model::load("facelivtv2-l.weights");
        }

        if (!det_model_.valid() || !rec_model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

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
                    if (rec_session && rec_session->valid()) {
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


} // namespace zhulong::face

// 注册为任务类型 3 (person_structure) 算法插件
ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(
    zhulong::face::FaceRecognitionPlugin,
    "FaceRecognition",
    "1.0.0"
)
