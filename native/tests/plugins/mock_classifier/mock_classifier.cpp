#include <zhulong/algo.hpp>

class MockClassifierPlugin {
public:
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        model_ = zhulong::Model::load("mock_classifier.weights");
        if (!model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        return model_.create_session(worker_id);
    }

    std::vector<zhulong::Classification> classify(zhulong::Session& session, const zhulong::Image& image) {
        zhulong::Tensor input_tensor;
        zhulong::LetterboxParams params;
        params.target_width = 224;
        params.target_height = 224;
        zhulong_status_t rc = zhulong::ops::letterbox(image, input_tensor, params);
        if (rc != ZHULONG_PLUGIN_OK) {
            return {};
        }

        auto outputs = session.forward({input_tensor});
        if (outputs.empty()) {
            return {};
        }

        zhulong::Classification result;
        result.class_id = 7;
        result.confidence = 0.98f;
        result.label = "helmet_detected";
        return {result};
    }

private:
    zhulong::Model model_;
};

ZHULONG_REGISTER_CLASSIFIER_PLUGIN(MockClassifierPlugin, "MockClassifier", "1.0.0")
