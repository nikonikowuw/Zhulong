#include <zhulong/algo.hpp>

class MockDetectorPlugin {
public:
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        model_ = zhulong::Model::load("mock_detector.weights");
        if (!model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        return model_.create_session(worker_id);
    }

    std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& image) {
        zhulong::Tensor input_tensor;
        zhulong::LetterboxParams params;
        params.target_width = 640;
        params.target_height = 640;
        zhulong_status_t rc = zhulong::ops::letterbox(image, input_tensor, params);
        if (rc != ZHULONG_PLUGIN_OK) {
            return {};
        }

        auto outputs = session.forward({input_tensor});
        if (outputs.empty()) {
            return {};
        }

        // 构造检测结果
        zhulong::Detection det;
        det.x = 100.0f;
        det.y = 120.0f;
        det.w = 50.0f;
        det.h = 80.0f;
        det.confidence = 0.92f;
        det.class_id = 1;
        det.label = "person";

        return zhulong::ops::nms({det}, 0.45f);
    }

private:
    zhulong::Model model_;
};

ZHULONG_REGISTER_DETECTOR_PLUGIN(MockDetectorPlugin, "MockDetector", "1.0.0")
