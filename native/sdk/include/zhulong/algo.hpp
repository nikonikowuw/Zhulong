#ifndef ZHULONG_ALGO_HPP
#define ZHULONG_ALGO_HPP

#include <zhulong/plugin.h>
#include <algorithm/runtime/runtime_factory.hpp>
#include <algorithm/runtime/vision_backend.hpp>
#include <algorithm/runtime/tensor.hpp>

#include <string>
#include <vector>
#include <memory>
#include <cstring>
#include <iostream>

namespace zhulong {

// 图像只读视图包装
class Image {
public:
    explicit Image(const zhulong_frame_view& view) : view_(view) {}

    int width() const noexcept { return view_.width; }
    int height() const noexcept { return view_.height; }
    zhulong_pixel_format_t format() const noexcept { return view_.format; }
    uint64_t frame_id() const noexcept { return view_.frame_id; }
    uint64_t pts() const noexcept { return view_.pts; }
    const zhulong_frame_view& raw_view() const noexcept { return view_; }

private:
    const zhulong_frame_view& view_;
};

// 目标检测框
struct Detection {
    float x{0.0f};
    float y{0.0f};
    float w{0.0f};
    float h{0.0f};
    float confidence{0.0f};
    int32_t class_id{0};
    std::string label;
};

// 图像分类属性
struct Classification {
    int32_t class_id{0};
    float confidence{0.0f};
    std::string label;
};

// 推理会话 (单 Worker 独占执行)
class Session {
public:
    Session() = default;
    Session(std::shared_ptr<IInferenceBackend> backend,
            std::unique_ptr<SessionContext> ctx)
        : backend_(std::move(backend)), ctx_(std::move(ctx)) {}

    bool valid() const noexcept { return backend_ && ctx_; }

    std::vector<Tensor> forward(const std::vector<Tensor>& inputs) {
        if (!valid()) return {};
        std::vector<Tensor> outputs;
        zhulong_status_t rc = backend_->forward(ctx_.get(), inputs, outputs);
        if (rc != ZHULONG_PLUGIN_OK) {
            return {};
        }
        return outputs;
    }

private:
    std::shared_ptr<IInferenceBackend> backend_;
    std::unique_ptr<SessionContext> ctx_;
};

// 模型资源组 (整包共享不可变权重)
class Model {
public:
    Model() = default;
    explicit Model(std::shared_ptr<ModelAsset> asset, std::shared_ptr<IInferenceBackend> backend)
        : asset_(std::move(asset)), backend_(std::move(backend)) {}

    static Model load(const std::string& model_path_or_config) {
        auto backend = get_default_inference_backend();
        if (!backend) return Model();
        auto asset = backend->load_model(model_path_or_config);
        return Model(std::move(asset), std::move(backend));
    }

    bool valid() const noexcept { return asset_ && backend_; }

    Session create_session(uint32_t worker_id = 0) {
        if (!valid()) return Session();
        auto session_ctx = backend_->create_session(asset_, worker_id);
        return Session(backend_, std::move(session_ctx));
    }

private:
    std::shared_ptr<ModelAsset> asset_;
    std::shared_ptr<IInferenceBackend> backend_;
};

// 硬件加速图像与后处理算子工具箱
namespace ops {

inline zhulong_status_t letterbox(const Image& image, Tensor& dst, LetterboxParams& params) {
    auto vision = get_default_vision_backend();
    if (!vision) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    return vision->letterbox(image.raw_view(), dst, params);
}

inline std::vector<Detection> nms(const std::vector<Detection>& detections, float iou_threshold = 0.45f) {
    if (detections.empty()) return {};
    std::vector<zhulong_bbox> c_boxes(detections.size());
    for (size_t i = 0; i < detections.size(); ++i) {
        c_boxes[i].x = detections[i].x;
        c_boxes[i].y = detections[i].y;
        c_boxes[i].width = detections[i].w;
        c_boxes[i].height = detections[i].h;
        c_boxes[i].confidence = detections[i].confidence;
        c_boxes[i].class_id = detections[i].class_id;
        std::strncpy(c_boxes[i].label, detections[i].label.c_str(), sizeof(c_boxes[i].label) - 1);
        c_boxes[i].label[sizeof(c_boxes[i].label) - 1] = '\0';
    }

    auto kept = zhulong::nms(c_boxes, iou_threshold);
    std::vector<Detection> result(kept.size());
    for (size_t i = 0; i < kept.size(); ++i) {
        result[i].x = kept[i].x;
        result[i].y = kept[i].y;
        result[i].w = kept[i].width;
        result[i].h = kept[i].height;
        result[i].confidence = kept[i].confidence;
        result[i].class_id = kept[i].class_id;
        result[i].label = kept[i].label;
    }
    return result;
}

} // namespace ops

} // namespace zhulong

// ==============================================================================
// 导出胶水宏：将用户 C++ 类自动包装为纯 C ABI 导出入口
// ==============================================================================

#define ZHULONG_REGISTER_DETECTOR_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
extern "C" { \
\
struct zhulong_model_group_t { \
    std::unique_ptr<PluginClass> plugin; \
}; \
\
struct zhulong_worker_ctx_t { \
    zhulong_model_group_t* model_grp{nullptr}; \
    zhulong::Session session; \
}; \
\
struct zhulong_task_ctx_t { \
    zhulong_worker_ctx_t* worker{nullptr}; \
    uint64_t task_id{0}; \
}; \
\
static zhulong_status_t plugin_model_create(const char* config_json, zhulong_model_group_h* out_model) { \
    if (!out_model) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT; \
    try { \
        auto mg = std::make_unique<zhulong_model_group_t>(); \
        mg->plugin = std::make_unique<PluginClass>(); \
        zhulong_status_t rc = mg->plugin->init(config_json ? config_json : ""); \
        if (rc != ZHULONG_PLUGIN_OK) return rc; \
        *out_model = reinterpret_cast<zhulong_model_group_h>(mg.release()); \
        return ZHULONG_PLUGIN_OK; \
    } catch (...) { \
        return ZHULONG_PLUGIN_ERR_INTERNAL; \
    } \
} \
\
static void plugin_model_destroy(zhulong_model_group_h model) { \
    try { \
        delete reinterpret_cast<zhulong_model_group_t*>(model); \
    } catch (...) {} \
} \
\
static zhulong_status_t plugin_worker_create(zhulong_model_group_h model, uint32_t worker_id, zhulong_worker_ctx_h* out_worker) { \
    if (!model || !out_worker) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT; \
    try { \
        auto mg = reinterpret_cast<zhulong_model_group_t*>(model); \
        auto w = std::make_unique<zhulong_worker_ctx_t>(); \
        w->model_grp = mg; \
        w->session = mg->plugin->create_session(worker_id); \
        *out_worker = reinterpret_cast<zhulong_worker_ctx_h>(w.release()); \
        return ZHULONG_PLUGIN_OK; \
    } catch (...) { \
        return ZHULONG_PLUGIN_ERR_INTERNAL; \
    } \
} \
\
static void plugin_worker_destroy(zhulong_worker_ctx_h worker) { \
    try { \
        delete reinterpret_cast<zhulong_worker_ctx_t*>(worker); \
    } catch (...) {} \
} \
\
static zhulong_status_t plugin_task_open(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task) { \
    if (!worker || !out_task) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT; \
    try { \
        auto t = std::make_unique<zhulong_task_ctx_t>(); \
        t->worker = reinterpret_cast<zhulong_worker_ctx_t*>(worker); \
        t->task_id = task_id; \
        *out_task = reinterpret_cast<zhulong_task_ctx_h>(t.release()); \
        return ZHULONG_PLUGIN_OK; \
    } catch (...) { \
        return ZHULONG_PLUGIN_ERR_INTERNAL; \
    } \
} \
\
static void plugin_task_close(zhulong_task_ctx_h task) { \
    try { \
        delete reinterpret_cast<zhulong_task_ctx_t*>(task); \
    } catch (...) {} \
} \
\
static zhulong_status_t plugin_process(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result) { \
    if (!task || !frame || !out_result) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT; \
    try { \
        auto t = reinterpret_cast<zhulong_task_ctx_t*>(task); \
        zhulong::Image img(*frame); \
        std::vector<zhulong::Detection> dets = t->worker->model_grp->plugin->detect(t->worker->session, img); \
        \
        auto res = new zhulong_inference_result(); \
        res->frame_id = frame->frame_id; \
        res->task_id = t->task_id; \
        res->result_type = ZHULONG_RESULT_TYPE_DETECTION; \
        res->count = static_cast<uint32_t>(dets.size()); \
        if (!dets.empty()) { \
            auto boxes = new zhulong_bbox[dets.size()]; \
            for (size_t i = 0; i < dets.size(); ++i) { \
                boxes[i].x = dets[i].x; \
                boxes[i].y = dets[i].y; \
                boxes[i].width = dets[i].w; \
                boxes[i].height = dets[i].h; \
                boxes[i].confidence = dets[i].confidence; \
                boxes[i].class_id = dets[i].class_id; \
                std::strncpy(boxes[i].label, dets[i].label.c_str(), sizeof(boxes[i].label) - 1); \
                boxes[i].label[sizeof(boxes[i].label) - 1] = '\0'; \
            } \
            res->data = boxes; \
            res->internal_token = boxes; \
        } else { \
            res->data = nullptr; \
            res->internal_token = nullptr; \
        } \
        *out_result = res; \
        return ZHULONG_PLUGIN_OK; \
    } catch (...) { \
        return ZHULONG_PLUGIN_ERR_INTERNAL; \
    } \
} \
\
static void plugin_result_release(zhulong_inference_result* result) { \
    if (!result) return; \
    try { \
        if (result->internal_token) { \
            delete[] reinterpret_cast<zhulong_bbox*>(result->internal_token); \
        } \
        delete result; \
    } catch (...) {} \
} \
\
zhulong_status_t zhulong_plugin_get_api(uint32_t requested_version, void* out_api_table, size_t table_size) { \
    if (requested_version != 1) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED; \
    if (!out_api_table || table_size < sizeof(zhulong_plugin_api_v1)) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT; \
    try { \
        auto api = reinterpret_cast<zhulong_plugin_api_v1*>(out_api_table); \
        std::memset(api, 0, sizeof(*api)); \
        api->struct_size = static_cast<uint32_t>(sizeof(zhulong_plugin_api_v1)); \
        api->api_version = 1; \
        api->plugin_name = PluginNameStr; \
        api->plugin_version = PluginVersionStr; \
        api->model_create = plugin_model_create; \
        api->model_destroy = plugin_model_destroy; \
        api->worker_create = plugin_worker_create; \
        api->worker_destroy = plugin_worker_destroy; \
        api->task_open = plugin_task_open; \
        api->task_close = plugin_task_close; \
        api->process = plugin_process; \
        api->result_release = plugin_result_release; \
        return ZHULONG_PLUGIN_OK; \
    } catch (...) { \
        return ZHULONG_PLUGIN_ERR_INTERNAL; \
    } \
} \
}

#endif /* ZHULONG_ALGO_HPP */
