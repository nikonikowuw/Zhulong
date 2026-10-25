#include <zhulong/algo.hpp>

class MockClassifierPlugin {
public:
    // ⚠️ 夹具插件：不加载任何模型，也不依赖任何推理后端。
    //   曾经它调用 Model::load("<name>.weights")：在旧的 cpu 桩后端下侥幸成功，
    //   在 onnx/rknn 真实后端下必然 model_create 失败，导致同一套回归在换后端后假红/假绿。现在夹具不加载任何模型。
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        (void)worker_id;
        return zhulong::Session();
    }

    std::vector<zhulong::Classification> classify(zhulong::Session& session, const zhulong::Image& image) {
        // 夹具输出与图像内容无关：不跑推理，也不依赖任何后端
        (void)session;
        (void)image;

        zhulong::Classification result;
        result.class_id = 7;
        result.confidence = 0.98f;
        result.label = "helmet_detected";
        return {result};
    }
};

ZHULONG_REGISTER_CLASSIFIER_PLUGIN(MockClassifierPlugin, "MockClassifier", "1.0.0")
