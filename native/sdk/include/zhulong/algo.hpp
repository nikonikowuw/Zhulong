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
// Plugin C ABI adapters
// ==============================================================================

namespace zhulong {
namespace detail {

struct DetectionResultAdapter {
    using item_type = zhulong_bbox;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_DETECTION;

    template<typename PluginClass>
    static std::vector<Detection> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.detect(session, image);
    }

    static void serialize(const Detection& source, item_type& destination) {
        destination = {};
        destination.x = source.x;
        destination.y = source.y;
        destination.width = source.w;
        destination.height = source.h;
        destination.confidence = source.confidence;
        destination.class_id = source.class_id;
        std::strncpy(destination.label, source.label.c_str(), sizeof(destination.label) - 1);
    }
};

struct ClassificationResultAdapter {
    using item_type = zhulong_classification_item;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_CLASSIFICATION;

    template<typename PluginClass>
    static std::vector<Classification> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.classify(session, image);
    }

    static void serialize(const Classification& source, item_type& destination) {
        destination = {};
        destination.class_id = source.class_id;
        destination.confidence = source.confidence;
        std::strncpy(destination.label, source.label.c_str(), sizeof(destination.label) - 1);
    }
};

template<typename PluginClass, typename ResultAdapter>
struct PluginApi {
    struct ModelGroup {
        std::unique_ptr<PluginClass> plugin;
    };

    struct WorkerContext {
        ModelGroup* model_group{nullptr};
        Session session;
    };

    struct TaskContext {
        WorkerContext* worker{nullptr};
        uint64_t task_id{0};
    };

    static zhulong_status_t model_create(const char* config_json, zhulong_model_group_h* out_model) {
        if (!out_model) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        *out_model = nullptr;
        try {
            auto model_group = std::make_unique<ModelGroup>();
            model_group->plugin = std::make_unique<PluginClass>();
            zhulong_status_t rc = model_group->plugin->init(config_json ? config_json : "");
            if (rc != ZHULONG_PLUGIN_OK) return rc;
            *out_model = reinterpret_cast<zhulong_model_group_h>(model_group.release());
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }

    static void model_destroy(zhulong_model_group_h model) {
        try {
            delete reinterpret_cast<ModelGroup*>(model);
        } catch (...) {}
    }

    static zhulong_status_t worker_create(zhulong_model_group_h model, uint32_t worker_id,
                                          zhulong_worker_ctx_h* out_worker) {
        if (!model || !out_worker) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        *out_worker = nullptr;
        try {
            auto* model_group = reinterpret_cast<ModelGroup*>(model);
            auto worker = std::make_unique<WorkerContext>();
            worker->model_group = model_group;
            worker->session = model_group->plugin->create_session(worker_id);
            *out_worker = reinterpret_cast<zhulong_worker_ctx_h>(worker.release());
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }

    static void worker_destroy(zhulong_worker_ctx_h worker) {
        try {
            delete reinterpret_cast<WorkerContext*>(worker);
        } catch (...) {}
    }

    static zhulong_status_t task_open(zhulong_worker_ctx_h worker, uint64_t task_id,
                                      zhulong_task_ctx_h* out_task) {
        if (!worker || !out_task) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        *out_task = nullptr;
        try {
            auto task = std::make_unique<TaskContext>();
            task->worker = reinterpret_cast<WorkerContext*>(worker);
            task->task_id = task_id;
            *out_task = reinterpret_cast<zhulong_task_ctx_h>(task.release());
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }

    static void task_close(zhulong_task_ctx_h task) {
        try {
            delete reinterpret_cast<TaskContext*>(task);
        } catch (...) {}
    }

    static zhulong_status_t process(zhulong_task_ctx_h task, const zhulong_frame_view* frame,
                                    zhulong_inference_result** out_result) {
        if (!task || !frame || !out_result) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        *out_result = nullptr;
        try {
            auto* task_context = reinterpret_cast<TaskContext*>(task);
            Image image(*frame);
            auto values = ResultAdapter::infer(*task_context->worker->model_group->plugin,
                                               task_context->worker->session, image);
            auto result = std::make_unique<zhulong_inference_result>();
            result->frame_id = frame->frame_id;
            result->task_id = task_context->task_id;
            result->result_type = ResultAdapter::result_type;
            result->count = static_cast<uint32_t>(values.size());

            std::unique_ptr<typename ResultAdapter::item_type[]> items;
            if (!values.empty()) {
                items = std::make_unique<typename ResultAdapter::item_type[]>(values.size());
                for (size_t i = 0; i < values.size(); ++i) {
                    ResultAdapter::serialize(values[i], items[i]);
                }
            }
            result->data = items.get();
            result->internal_token = items.get();
            *out_result = result.release();
            items.release();
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }

    static void result_release(zhulong_inference_result* result) {
        if (!result) return;
        try {
            delete[] static_cast<typename ResultAdapter::item_type*>(result->internal_token);
            delete result;
        } catch (...) {}
    }

    static zhulong_status_t get_api(uint32_t requested_version, void* out_api_table, size_t table_size,
                                    const char* plugin_name, const char* plugin_version) {
        if (requested_version != 1) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        if (!out_api_table || table_size < sizeof(zhulong_plugin_api_v1)) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }
        try {
            auto* api = reinterpret_cast<zhulong_plugin_api_v1*>(out_api_table);
            std::memset(api, 0, sizeof(*api));
            api->struct_size = static_cast<uint32_t>(sizeof(zhulong_plugin_api_v1));
            api->api_version = 1;
            api->plugin_name = plugin_name;
            api->plugin_version = plugin_version;
            api->model_create = model_create;
            api->model_destroy = model_destroy;
            api->worker_create = worker_create;
            api->worker_destroy = worker_destroy;
            api->task_open = task_open;
            api->task_close = task_close;
            api->process = process;
            api->result_release = result_release;
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }
};

} // namespace detail
} // namespace zhulong

#define ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, Adapter) \
extern "C" ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_plugin_get_api( \
    uint32_t requested_version, void* out_api_table, size_t table_size) { \
    return ::zhulong::detail::PluginApi<PluginClass, Adapter>::get_api( \
        requested_version, out_api_table, table_size, PluginNameStr, PluginVersionStr); \
}

#define ZHULONG_REGISTER_DETECTOR_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::DetectionResultAdapter)

#define ZHULONG_REGISTER_CLASSIFIER_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::ClassificationResultAdapter)

#endif /* ZHULONG_ALGO_HPP */
