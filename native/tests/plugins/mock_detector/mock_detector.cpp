#include <zhulong/algo.hpp>

class MockDetectorPlugin {
public:
    // ⚠️ 夹具插件：不加载任何模型，也不依赖任何推理后端。
    //   曾经它调用 Model::load("<name>.weights")：在 cpu_mock 桩后端下侥幸成功，
    //   在 onnx/rknn 真实后端下必然 model_create 失败，导致同一套回归在换后端后假红/假绿。
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        (void)worker_id;
        return zhulong::Session();
    }

    std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& image) {
        // 夹具输出与图像内容无关：不跑推理，也不依赖任何后端
        (void)session;
        (void)image;

        // 构造检测结果 (归一化 [x1, y1, x2, y2])
        zhulong::Detection det;
        det.box[0] = 0.15f;
        det.box[1] = 0.20f;
        det.box[2] = 0.35f;
        det.box[3] = 0.40f;
        det.confidence = 0.92f;
        det.class_id = 1;
        det.label = "person";

        return zhulong::ops::nms({det}, 0.45f);
    }
};

ZHULONG_REGISTER_DETECTOR_PLUGIN(MockDetectorPlugin, "MockDetector", "1.0.0")
