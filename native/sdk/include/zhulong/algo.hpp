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
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /* [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0] */
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

// 识别候选人
struct FaceCandidate {
    std::string person_id;
    std::string person_name;
    float similarity{0.0f};
};

// 关键点
struct Landmark {
    float x{0.0f};
    float y{0.0f};
};

// 单张人脸项
struct FaceItem {
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /* [x1, y1, x2, y2] 归一化坐标 */
    float quality{0.0f};
    std::vector<Landmark> landmarks;      /* 5 点关键点 */
    bool matched{false};
    float similarity{0.0f};
    bool is_fused{false};
    std::string person_id;
    std::string person_name;
    std::vector<FaceCandidate> candidates;
};

// 结构化人体项
struct PersonItem {
    int64_t track_id{0};
    float body_box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /* [x1, y1, x2, y2] 归一化坐标 */
    float body_score{0.0f};
    std::vector<FaceItem> faces;
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
        c_boxes[i].box[0] = detections[i].box[0];
        c_boxes[i].box[1] = detections[i].box[1];
        c_boxes[i].box[2] = detections[i].box[2];
        c_boxes[i].box[3] = detections[i].box[3];
        c_boxes[i].confidence = detections[i].confidence;
        c_boxes[i].class_id = detections[i].class_id;
        std::strncpy(c_boxes[i].label, detections[i].label.c_str(), sizeof(c_boxes[i].label) - 1);
        c_boxes[i].label[sizeof(c_boxes[i].label) - 1] = '\0';
    }

    auto kept = zhulong::nms(c_boxes, iou_threshold);
    std::vector<Detection> result(kept.size());
    for (size_t i = 0; i < kept.size(); ++i) {
        result[i].box[0] = kept[i].box[0];
        result[i].box[1] = kept[i].box[1];
        result[i].box[2] = kept[i].box[2];
        result[i].box[3] = kept[i].box[3];
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

static_assert(sizeof(zhulong_bbox) == 88, "zhulong_bbox must be 88 bytes");
static_assert(sizeof(zhulong_bbox) % 8 == 0, "zhulong_bbox must be 8-byte aligned");
static_assert(sizeof(zhulong_face_candidate) == 136, "zhulong_face_candidate must be 136 bytes");
static_assert(sizeof(zhulong_face_candidate) % 8 == 0, "zhulong_face_candidate must be 8-byte aligned");
static_assert(sizeof(zhulong_face_item) == 216, "zhulong_face_item must be 216 bytes");
static_assert(sizeof(zhulong_face_item) % 8 == 0, "zhulong_face_item must be 8-byte aligned");
static_assert(sizeof(zhulong_person_item) == 40, "zhulong_person_item must be 40 bytes");
static_assert(sizeof(zhulong_person_item) % 8 == 0, "zhulong_person_item must be 8-byte aligned");

struct DetectionResultAdapter {
    using item_type = zhulong_bbox;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_DETECTION;

    template<typename PluginClass>
    static std::vector<Detection> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.detect(session, image);
    }

    static void serialize(const Detection& source, item_type& destination) {
        destination = {};
        destination.box[0] = source.box[0];
        destination.box[1] = source.box[1];
        destination.box[2] = source.box[2];
        destination.box[3] = source.box[3];
        destination.confidence = source.confidence;
        destination.class_id = source.class_id;
        std::strncpy(destination.label, source.label.c_str(), sizeof(destination.label) - 1);
        destination.label[sizeof(destination.label) - 1] = '\0';
    }

    template<typename PluginClass>
    static void pack(PluginClass& plugin, Session& session, const Image& image,
                     zhulong_inference_result& out) {
        auto values = infer(plugin, session, image);
        out.count = static_cast<uint32_t>(values.size());
        if (values.empty()) {
            out.data = nullptr;
            out.internal_token = nullptr;
            return;
        }
        auto items = std::make_unique<item_type[]>(values.size());
        for (size_t i = 0; i < values.size(); ++i) {
            serialize(values[i], items[i]);
        }
        out.data = items.get();
        out.internal_token = items.release();
    }

    static void release(void* token) {
        delete[] static_cast<item_type*>(token);
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

    template<typename PluginClass>
    static void pack(PluginClass& plugin, Session& session, const Image& image,
                     zhulong_inference_result& out) {
        auto values = infer(plugin, session, image);
        out.count = static_cast<uint32_t>(values.size());
        if (values.empty()) {
            out.data = nullptr;
            out.internal_token = nullptr;
            return;
        }
        auto items = std::make_unique<item_type[]>(values.size());
        for (size_t i = 0; i < values.size(); ++i) {
            serialize(values[i], items[i]);
        }
        out.data = items.get();
        out.internal_token = items.release();
    }

    static void release(void* token) {
        delete[] static_cast<item_type*>(token);
    }
};

struct PersonStructureResultAdapter {
    using item_type = zhulong_person_item;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_PERSON_STRUCTURE;

    template<typename PluginClass>
    static std::vector<PersonItem> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.detect_persons(session, image);
    }

    template<typename PluginClass>
    static void pack(PluginClass& plugin, Session& session, const Image& image,
                     zhulong_inference_result& out) {
        auto persons = infer(plugin, session, image);
        size_t n_persons = persons.size();
        size_t n_faces = 0;
        size_t n_candidates = 0;

        for (const auto& p : persons) {
            n_faces += p.faces.size();
            for (const auto& f : p.faces) {
                n_candidates += f.candidates.size();
            }
        }

        if (n_persons == 0) {
            out.count = 0;
            out.data = nullptr;
            out.internal_token = nullptr;
            return;
        }

        size_t persons_bytes = n_persons * sizeof(zhulong_person_item);
        size_t faces_bytes = n_faces * sizeof(zhulong_face_item);
        size_t candidates_bytes = n_candidates * sizeof(zhulong_face_candidate);
        size_t total_bytes = persons_bytes + faces_bytes + candidates_bytes;

        uint8_t* arena = static_cast<uint8_t*>(std::malloc(total_bytes));
        if (!arena) {
            throw std::bad_alloc();
        }
        std::memset(arena, 0, total_bytes);

        auto* out_persons = reinterpret_cast<zhulong_person_item*>(arena);
        auto* out_faces = reinterpret_cast<zhulong_face_item*>(arena + persons_bytes);
        auto* out_candidates = reinterpret_cast<zhulong_face_candidate*>(arena + persons_bytes + faces_bytes);

        size_t face_idx = 0;
        size_t cand_idx = 0;

        for (size_t i = 0; i < n_persons; ++i) {
            const auto& src_p = persons[i];
            auto& dst_p = out_persons[i];

            dst_p.track_id = src_p.track_id;
            dst_p.body_box[0] = src_p.body_box[0];
            dst_p.body_box[1] = src_p.body_box[1];
            dst_p.body_box[2] = src_p.body_box[2];
            dst_p.body_box[3] = src_p.body_box[3];
            dst_p.body_score = src_p.body_score;
            dst_p.face_count = static_cast<uint32_t>(src_p.faces.size());

            if (dst_p.face_count > 0) {
                dst_p.faces = &out_faces[face_idx];
                for (size_t j = 0; j < src_p.faces.size(); ++j) {
                    const auto& src_f = src_p.faces[j];
                    auto& dst_f = out_faces[face_idx++];

                    dst_f.box[0] = src_f.box[0];
                    dst_f.box[1] = src_f.box[1];
                    dst_f.box[2] = src_f.box[2];
                    dst_f.box[3] = src_f.box[3];
                    dst_f.quality = src_f.quality;

                    size_t n_lm = std::min<size_t>(src_f.landmarks.size(), ZHULONG_FACE_LANDMARK_COUNT);
                    for (size_t k = 0; k < n_lm; ++k) {
                        dst_f.landmarks[k][0] = src_f.landmarks[k].x;
                        dst_f.landmarks[k][1] = src_f.landmarks[k].y;
                    }

                    dst_f.matched = src_f.matched ? 1 : 0;
                    dst_f.similarity = src_f.similarity;
                    dst_f.is_fused = src_f.is_fused ? 1 : 0;

                    std::strncpy(dst_f.person_id, src_f.person_id.c_str(), sizeof(dst_f.person_id) - 1);
                    dst_f.person_id[sizeof(dst_f.person_id) - 1] = '\0';
                    std::strncpy(dst_f.person_name, src_f.person_name.c_str(), sizeof(dst_f.person_name) - 1);
                    dst_f.person_name[sizeof(dst_f.person_name) - 1] = '\0';

                    dst_f.candidate_count = static_cast<uint32_t>(src_f.candidates.size());
                    if (dst_f.candidate_count > 0) {
                        dst_f.candidates = &out_candidates[cand_idx];
                        for (size_t k = 0; k < src_f.candidates.size(); ++k) {
                            const auto& src_c = src_f.candidates[k];
                            auto& dst_c = out_candidates[cand_idx++];

                            std::strncpy(dst_c.person_id, src_c.person_id.c_str(), sizeof(dst_c.person_id) - 1);
                            dst_c.person_id[sizeof(dst_c.person_id) - 1] = '\0';
                            std::strncpy(dst_c.person_name, src_c.person_name.c_str(), sizeof(dst_c.person_name) - 1);
                            dst_c.person_name[sizeof(dst_c.person_name) - 1] = '\0';
                            dst_c.similarity = src_c.similarity;
                        }
                    } else {
                        dst_f.candidates = nullptr;
                    }
                }
            } else {
                dst_p.faces = nullptr;
            }
        }

        out.count = static_cast<uint32_t>(n_persons);
        out.data = out_persons;
        out.internal_token = arena;
    }

    static void release(void* token) {
        if (token) {
            std::free(token);
        }
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
            auto result = std::make_unique<zhulong_inference_result>();
            result->frame_id = frame->frame_id;
            result->task_id = task_context->task_id;
            result->result_type = ResultAdapter::result_type;

            ResultAdapter::pack(*task_context->worker->model_group->plugin,
                                task_context->worker->session, image, *result);

            *out_result = result.release();
            return ZHULONG_PLUGIN_OK;
        } catch (...) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
    }

    static void result_release(zhulong_inference_result* result) {
        if (!result) return;
        try {
            ResultAdapter::release(result->internal_token);
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

#define ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::PersonStructureResultAdapter)

#endif /* ZHULONG_ALGO_HPP */
