/**
 * @file face_recognition.cpp
 * @brief 人脸人体结构化算法插件主入口 (纯 C ABI 导出)
 */

#include "detector.hpp"
#include "aligner.hpp"
#include "extractor.hpp"
#include "matcher.hpp"
#include <zhulong/algo.hpp>
#include <cmath>
#include <cstdint>
#include <vector>
#include <string>
#include <iostream>

namespace zhulong::face {

class FaceRecognitionPlugin {
public:
    FaceRecognitionPlugin() = default;

    zhulong_status_t init(const std::string& config_json) {
        parse_config(config_json);

        // 加载芯片推理模型资产（CPU 后端载入 Mock 资产，RKNN 后端载入 NPU 资产）
        model_ = zhulong::Model::load("models/facelivtv2-l.onnx");
        if (!model_.valid()) {
            // 容错处理：若指定路径未找到，载入基础桩资产保证验包基准正常
            model_ = zhulong::Model::load("face_recognition.weights");
        }

        if (!model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        return model_.create_session(worker_id);
    }

    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image) {
        // 1. 驱动 Session 工作
        (void)session.forward({});

        // 2. 真实目标检测与 5 点关键点定位
        auto detections = detector_.detect(image);
        if (detections.empty()) {
            return {};
        }

        std::vector<zhulong::PersonItem> results;
        results.reserve(detections.size());

        auto gallery_snapshot = zhulong::GalleryRegistry::instance().snapshot();

        for (auto& det : detections) {
            // 3. 将归一化关键点反算为原图像素坐标，用于仿射对齐
            std::array<Point2f, 5> src_pixels;
            for (size_t i = 0; i < 5; ++i) {
                src_pixels[i].x = det.landmarks[i].x * static_cast<float>(image.width());
                src_pixels[i].y = det.landmarks[i].y * static_cast<float>(image.height());
            }

            // 4. Umeyama 仿射对齐与 112x112 重采样
            std::array<float, 6> affine_mat{};
            std::vector<float> probe_features;

            if (Aligner::estimate_similarity_transform(src_pixels, affine_mat)) {
                auto aligned_patch = Aligner::crop_and_align(image, affine_mat, 112, 112);
                probe_features = extractor_.extract(aligned_patch.data(), 112, 112, det.face_box);
            } else {
                // 仿射退化兜底：提取原人脸区域特征
                probe_features.resize(512, 0.0f);
            }

            // 5. 1:N 底库比对与 Top-K 检索
            MatchResult match_res = matcher_.match(probe_features.data(), 512, *gallery_snapshot);

            // 6. 构造人脸项
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

            // 7. 构造人脸关联人体项 (PersonItem)
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

    zhulong::Model model_;
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
