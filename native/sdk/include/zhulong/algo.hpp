/**
 * @file algo.hpp
 * @brief Zhulong 算法插件现代 C++17 Header-only SDK 门面
 *
 * 为算法插件开发者提供现代 C++ 抽象门面（RAII 会话、图像只读视图、类型安全张量与算子工具箱），
 * 并在底层自动桥接至纯 C ABI（zhulong_plugin_api_v1），屏蔽底层 C 句柄管理与内存序列化细节。
 *
 * 核心架构与边界契约：
 * 1. 现代 C++ 门面：通过 Image / Session / Model / ops 接口编写算法，零裸指针管理；
 * 2. 纯 C ABI 物理隔离：通过宏（ZHULONG_REGISTER_*_PLUGIN）自动展开导出纯 C 符号 zhulong_plugin_get_api；
 * 3. 异常防爆门：所有对插件 C++ 方法的调用均在 PluginApi 适配器层以 try-catch 拦截，禁止异常穿越 .so 边界；
 * 4. 单块连续 Arena 打包：结构化多级嵌套结果在单个连续内存块中序列化，internal_token 记录首地址，
 *    通过单次 std::free 彻底释放整棵结果树，杜绝内存碎片与嵌套泄漏；
 * 5. 严格坐标归一化：所有目标检测框、人体框、人脸框与关键点均采用 [x1, y1, x2, y2] ∈ [0.0, 1.0] 归一化坐标。
 */

#ifndef ZHULONG_ALGO_HPP
#define ZHULONG_ALGO_HPP

#include <zhulong/plugin.h>
#include <algorithm/runtime/runtime_factory.hpp>
#include <algorithm/runtime/vision_backend.hpp>
#include <algorithm/runtime/tensor.hpp>
#include <algorithm/runtime/gallery_registry.hpp>

#include <string>
#include <vector>
#include <memory>
#include <cstring>
#include <iostream>

namespace zhulong {

using GalleryRegistry = zhulong::runtime::GalleryRegistry;
using FaceTemplate = zhulong::runtime::FaceTemplate;

/**
 * @brief 图像只读借用视图包装类
 *
 * 轻量封装底层的 zhulong_frame_view，提供面向对象的宽高、格式与时间戳查询。
 *
 * ⚠️ 生命周期与借用契约：
 *   - 本对象内部仅持有 zhulong_frame_view 的常量引用，不拥有底层像素内存；
 *   - 图像数据仅在 process 同步调用期间严格有效；
 *   - 插件若需在异步线程中持久持有图像数据，必须在 process 返回前完成私有内存深拷贝。
 */
class Image {
public:
    /**
     * @brief 构造函数，绑定底层只读图像帧视图
     * @param view 底层图像帧视图引用
     */
    explicit Image(const zhulong_frame_view& view) : view_(view) {}

    /** @brief 获取图像有效像素宽度 */
    int width() const noexcept { return view_.width; }

    /** @brief 获取图像有效像素高度 */
    int height() const noexcept { return view_.height; }

    /** @brief 获取图像像素色彩格式 */
    zhulong_pixel_format_t format() const noexcept { return view_.format; }

    /** @brief 获取视频帧序号 */
    uint64_t frame_id() const noexcept { return view_.frame_id; }

    /** @brief 获取源流显示时间戳 (PTS) */
    uint64_t pts() const noexcept { return view_.pts; }

    /** @brief 获取底层的原始只读帧视图引用 */
    const zhulong_frame_view& raw_view() const noexcept { return view_; }

private:
    const zhulong_frame_view& view_;
};

/**
 * @brief 目标检测结果项
 *
 * 表示单个目标检测框及其置信度、类别 ID 与标签。
 */
struct Detection {
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0]，左上角 (x1, y1)，右下角 (x2, y2) */
    float confidence{0.0f};               /**< 检测置信度 (0.0 ~ 1.0) */
    int32_t class_id{0};                  /**< 类别数字 ID */
    std::string label;                    /**< 类别名称字符串 */
};

/**
 * @brief 图像分类属性项
 */
struct Classification {
    int32_t class_id{0};                  /**< 分类数字 ID */
    float confidence{0.0f};               /**< 分类置信度 (0.0 ~ 1.0) */
    std::string label;                    /**< 类别名称字符串 */
};

/**
 * @brief 人脸识别 Top-K 候选人项
 */
struct FaceCandidate {
    std::string person_id;                /**< 底库人员唯一标识符 */
    std::string person_name;              /**< 底库人员姓名 */
    float similarity{0.0f};               /**< 匹配置信度分值 / 相似度 (0.0 ~ 1.0) */
};

/**
 * @brief 人脸关键点坐标项
 */
struct Landmark {
    float x{0.0f};                        /**< 归一化 X 坐标 [0.0, 1.0] */
    float y{0.0f};                        /**< 归一化 Y 坐标 [0.0, 1.0] */
};

/**
 * @brief 单张人脸项
 *
 * 包含单个人脸的位置框、质量分、关键点、底库比对命中结果以及 Top-K 候选人列表。
 */
struct FaceItem {
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 归一化坐标 [0.0, 1.0] */
    float quality{0.0f};                   /**< 人脸质量分 (0.0 ~ 1.0) */
    std::vector<Landmark> landmarks;       /**< 5 点关键点坐标（标准顺序：左眼、右眼、鼻尖、左嘴角、右嘴角） */
    bool matched{false};                   /**< 是否命中底库 (true 为命中，false 为未命中) */
    float similarity{0.0f};                /**< Top-1 匹配相似度 (0.0 ~ 1.0) */
    bool is_fused{false};                  /**< 是否多帧融合结果 (预留字段) */
    std::string person_id;                 /**< Top-1 人员唯一标识符 (未命中时为空字符串) */
    std::string person_name;               /**< Top-1 人员姓名 (未命中时为空字符串) */
    std::vector<FaceCandidate> candidates; /**< Top-K 候选人列表，按相似度降序排列 */
};

/**
 * @brief 结构化人体项 (1 个人体挂载 0~N 张人脸)
 *
 * 纯人脸场景退化语义说明：
 *   - 在纯人脸检测/识别场景下，每个检出人脸映射为一个 PersonItem；
 *   - track_id 填 0；
 *   - body_box 填对应 face_box 的归一化坐标；
 *   - body_score 填 1.0f；
 *   - faces 包含单张人脸项。
 */
struct PersonItem {
    int64_t track_id{0};                       /**< 人体跟踪 ID (纯人脸场景填 0) */
    float body_box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /**< [x1, y1, x2, y2] 归一化坐标 (纯人脸场景同 face_box) */
    float body_score{0.0f};                    /**< 人体检出置信度 (纯人脸场景填 1.0f) */
    std::vector<FaceItem> faces;               /**< 关联的人脸列表 (0~N 张) */
};

/**
 * @brief 推理会话 (单 Worker 独占执行)
 *
 * 封装底层的硬件执行上下文（SessionContext），负责在单线程内驱动硬件加速推理。
 *
 * ⚠️ 并发约束：
 *   - 单个 Session 实例必须由单个 Worker 线程独占调用，严禁跨线程并发调用 forward；
 *   - 不同的 Worker 线程通过 Model::create_session 创建各自独立的 Session 实例。
 */
class Session {
public:
    Session() = default;

    /**
     * @brief 构造函数，绑定推理后端与会话上下文
     * @param backend 硬件推理后端指针
     * @param ctx 线程独占的会话上下文所有权
     */
    Session(std::shared_ptr<IInferenceBackend> backend,
            std::unique_ptr<SessionContext> ctx)
        : backend_(std::move(backend)), ctx_(std::move(ctx)) {}

    /** @brief 检查当前会话是否已成功初始化且可用 */
    bool valid() const noexcept { return backend_ && ctx_; }

    /**
     * @brief 同步执行模型前向推理
     *
     * ⚠️ 失败语义：
     *   - 若会话无效或后端执行返回错误，本函数返回空 vector；
     *   - 外部调用者需注意：返回空 vector 无法区分「硬件执行失败」与「模型合法产出 0 个张量」，
     *     在生产调用前应通过 valid() 确保会话有效，并检查输入合法性。
     *
     * @param inputs 输入张量列表
     * @return std::vector<Tensor> 推理输出张量列表；失败时返回空 vector
     */
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

/**
 * @brief 模型资源组 (整包共享不可变权重)
 *
 * 负责加载和持有模型的静态权重资产（ModelAsset），在整个算法包的多线程间共享。
 * 通过 create_session 方法为各 Worker 线程派生各自独立的执行会话。
 */
class Model {
public:
    Model() = default;

    /**
     * @brief 显式构造函数，绑定模型资产与推理后端
     * @param asset 模型资产共享指针
     * @param backend 推理后端共享指针
     */
    explicit Model(std::shared_ptr<ModelAsset> asset, std::shared_ptr<IInferenceBackend> backend)
        : asset_(std::move(asset)), backend_(std::move(backend)) {}

    /**
     * @brief 加载模型资产并初始化推理后端
     *
     * 内部自动获取当前平台编译配置下的默认推理后端（如 CPU / RKNN / CANN）。
     * 加载失败时不抛出异常，而是返回 valid() == false 的空 Model 实例。
     *
     * @param model_path_or_config 模型权重路径或 JSON 格式的配置字符串
     * @return Model 构造的模型资源组实例；加载失败时 valid() 为 false
     */
    static Model load(const std::string& model_path_or_config) {
        auto backend = get_default_inference_backend();
        if (!backend) return Model();
        auto asset = backend->load_model(model_path_or_config);
        return Model(std::move(asset), std::move(backend));
    }

    /** @brief 检查模型资产是否已成功加载且可用 */
    bool valid() const noexcept { return asset_ && backend_; }

    /**
     * @brief 为指定的 Worker 线程创建独立的执行会话
     *
     * 底层后端根据具体硬件架构实现权重共享（如在 RKNN 上通过 rknn_dup_context 共享物理内存），
     * 同时为该 worker_id 分配独立的运行期临时工作区。
     *
     * @param worker_id 执行线程/Worker 的唯一编号
     * @return Session 构造的独立执行会话；若模型无效则返回 valid() == false 的空 Session
     */
    Session create_session(uint32_t worker_id = 0) {
        if (!valid()) return Session();
        auto session_ctx = backend_->create_session(asset_, worker_id);
        return Session(backend_, std::move(session_ctx));
    }

private:
    std::shared_ptr<ModelAsset> asset_;
    std::shared_ptr<IInferenceBackend> backend_;
};

/**
 * @brief 硬件加速图像与后处理算子工具箱
 */
namespace ops {

/**
 * @brief 图像 Letterbox 预处理算子 (等比缩放、居中补边与色彩格式转换)
 *
 * 将输入图像按等比缩放至目标尺寸，并在四周填充灰色边（默认 114 补边值），
 * 同时转换为目标模型所需的像素色彩格式（如 RGB24）。
 *
 * @param image 输入图像只读视图
 * @param[out] dst 输出的目标张量 (输出布局为 HWC 紧凑排列)
 * @param[in,out] params 预处理参数；输入期望宽高与色彩格式，算子执行后回填 scale、pad_left、pad_top 等几何信息
 * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；ZHULONG_PLUGIN_ERR_NOT_SUPPORTED 平台不支持视觉加速
 */
inline zhulong_status_t letterbox(const Image& image, Tensor& dst, LetterboxParams& params) {
    auto vision = get_default_vision_backend();
    if (!vision) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    return vision->letterbox(image.raw_view(), dst, params);
}

/**
 * @brief 目标检测框非极大值抑制算子 (NMS)
 *
 * 采用逐框贪心抑制策略消除重叠框。最坏时间复杂度为 O(N^2)。
 *
 * @param detections 候选目标检测框列表（要求 box 坐标必须已归一化到 [0.0, 1.0]）
 * @param iou_threshold IoU 重叠抑制阈值 (默认 0.45f)
 * @return std::vector<Detection> 抑制过滤后保留的目标检测框列表
 */
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

// 静态断言：强制校验 C ABI 跨边界结构体的尺寸与 8 字节自然对齐契约
static_assert(sizeof(zhulong_bbox) == 88, "zhulong_bbox must be 88 bytes");
static_assert(sizeof(zhulong_bbox) % 8 == 0, "zhulong_bbox must be 8-byte aligned");
static_assert(sizeof(zhulong_face_candidate) == 136, "zhulong_face_candidate must be 136 bytes");
static_assert(sizeof(zhulong_face_candidate) % 8 == 0, "zhulong_face_candidate must be 8-byte aligned");
static_assert(sizeof(zhulong_face_item) == 216, "zhulong_face_item must be 216 bytes");
static_assert(sizeof(zhulong_face_item) % 8 == 0, "zhulong_face_item must be 8-byte aligned");
static_assert(sizeof(zhulong_person_item) == 40, "zhulong_person_item must be 40 bytes");
static_assert(sizeof(zhulong_person_item) % 8 == 0, "zhulong_person_item must be 8-byte aligned");

/**
 * @brief 通用目标检测结果 C ABI 序列化适配器
 *
 * 负责将 C++ 的 std::vector<Detection> 序列化为连续的 zhulong_bbox[] 数组，
 * 并以 internal_token 持有数组所有权，在 result_release 时通过 delete[] 释放。
 */
struct DetectionResultAdapter {
    using item_type = zhulong_bbox;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_DETECTION;

    /** @brief 调用插件类的 detect 方法执行检测推理 */
    template<typename PluginClass>
    static std::vector<Detection> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.detect(session, image);
    }

    /** @brief 将单个 Detection 结构体序列化至 C ABI zhulong_bbox */
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

    /** @brief 执行推理并将检测结果打包为 C ABI zhulong_inference_result 结构体 */
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

    /** @brief 释放由 pack 分配的 zhulong_bbox[] 数组内存 */
    static void release(void* token) {
        delete[] static_cast<item_type*>(token);
    }
};

/**
 * @brief 图像分类结果 C ABI 序列化适配器
 *
 * 负责将 C++ 的 std::vector<Classification> 序列化为连续的 zhulong_classification_item[] 数组。
 */
struct ClassificationResultAdapter {
    using item_type = zhulong_classification_item;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_CLASSIFICATION;

    /** @brief 调用插件类的 classify 方法执行分类推理 */
    template<typename PluginClass>
    static std::vector<Classification> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.classify(session, image);
    }

    /** @brief 将单个 Classification 结构体序列化至 C ABI zhulong_classification_item */
    static void serialize(const Classification& source, item_type& destination) {
        destination = {};
        destination.class_id = source.class_id;
        destination.confidence = source.confidence;
        std::strncpy(destination.label, source.label.c_str(), sizeof(destination.label) - 1);
    }

    /** @brief 执行推理并将分类结果打包为 C ABI zhulong_inference_result 结构体 */
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

    /** @brief 释放由 pack 分配的 zhulong_classification_item[] 数组内存 */
    static void release(void* token) {
        delete[] static_cast<item_type*>(token);
    }
};

/**
 * @brief 结构化人体/人脸结果 C ABI 序列化适配器 (单块连续 Arena 内存打包)
 *
 * 针对 PersonItem -> FaceItem -> FaceCandidate 多级树状嵌套结构，
 * 采用单块连续内存（Contiguous Arena Packing）打包算法：
 * 1. 统计人体总数、人脸总数与候选人总数，计算单块内存总字节大小：
 *    total_bytes = persons_bytes + faces_bytes + candidates_bytes；
 * 2. 单次 std::malloc(total_bytes) 分配连续 Arena 内存块并清零；
 * 3. 将 Arena 划分为三段：首部为 zhulong_person_item[]，中部为 zhulong_face_item[]，尾部为 zhulong_face_candidate[]；
 * 4. 各层级的子指针（faces、candidates）直接指向 Arena 内部的绝对地址偏移；
 * 5. internal_token 记录 Arena 首地址指针；
 * 6. 释放时由 release(token) 执行单次 std::free，彻底回收整棵结构树，杜绝内存碎片与嵌套泄漏。
 */
struct PersonStructureResultAdapter {
    using item_type = zhulong_person_item;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_PERSON_STRUCTURE;

    /** @brief 调用插件类的 detect_persons 方法执行人体/人脸结构化推理 */
    template<typename PluginClass>
    static std::vector<PersonItem> infer(PluginClass& plugin, Session& session, const Image& image) {
        return plugin.detect_persons(session, image);
    }

    /** @brief 将多级嵌套人体/人脸结构以单块连续 Arena 内存打包输出至 zhulong_inference_result */
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

    /** @brief 释放单块连续 Arena 内存 */
    static void release(void* token) {
        if (token) {
            std::free(token);
        }
    }
};

/**
 * @brief 插件 C ABI 桥接模板类
 *
 * 将开发者编写的 C++ 插件类（PluginClass）与结果序列化适配器（ResultAdapter）
 * 组装为符合 zhulong_plugin_api_v1 规范的函数指针表，并实现异常防爆门。
 *
 * @tparam PluginClass 开发者编写的算法插件类
 * @tparam ResultAdapter 结果打包适配器
 */
template<typename PluginClass, typename ResultAdapter>
struct PluginApi {
    /** @brief 宿主不透明句柄 zhulong_model_group_h 对应的内部包装类型 */
    struct ModelGroup {
        std::unique_ptr<PluginClass> plugin;
    };

    /** @brief 宿主不透明句柄 zhulong_worker_ctx_h 对应的内部包装类型 */
    struct WorkerContext {
        ModelGroup* model_group{nullptr};
        Session session;
    };

    /** @brief 宿主不透明句柄 zhulong_task_ctx_h 对应的内部包装类型 */
    struct TaskContext {
        WorkerContext* worker{nullptr};
        uint64_t task_id{0};
    };

    /** @brief 创建插件模型资源组，调用 PluginClass::init(config_json) */
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

    /** @brief 销毁插件模型资源组 */
    static void model_destroy(zhulong_model_group_h model) {
        try {
            delete reinterpret_cast<ModelGroup*>(model);
        } catch (...) {}
    }

    /** @brief 创建 Worker 执行上下文，调用 PluginClass::create_session(worker_id) */
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

    /** @brief 销毁 Worker 执行上下文 */
    static void worker_destroy(zhulong_worker_ctx_h worker) {
        try {
            delete reinterpret_cast<WorkerContext*>(worker);
        } catch (...) {}
    }

    /** @brief 打开单路摄像机时序任务状态 */
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

    /** @brief 关闭摄像机任务状态 */
    static void task_close(zhulong_task_ctx_h task) {
        try {
            delete reinterpret_cast<TaskContext*>(task);
        } catch (...) {}
    }

    /** @brief 同步推理执行：包装 Image，调用适配器 pack，输出推理结果 */
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

    /** @brief 释放推理结果及其关联的 Arena 内存 */
    static void result_release(zhulong_inference_result* result) {
        if (!result) return;
        try {
            ResultAdapter::release(result->internal_token);
            delete result;
        } catch (...) {}
    }

    /** @brief 填充并获取 zhulong_plugin_api_v1 接口函数表 */
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

/**
 * @brief 使用自定义结果适配器注册插件动态库导出符号
 *
 * 展开导出 zhulong_plugin_get_api 函数，将 PluginClass 绑定到指定的 Adapter。
 */
#define ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, Adapter) \
extern "C" ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_plugin_get_api( \
    uint32_t requested_version, void* out_api_table, size_t table_size) { \
    return ::zhulong::detail::PluginApi<PluginClass, Adapter>::get_api( \
        requested_version, out_api_table, table_size, PluginNameStr, PluginVersionStr); \
}

/**
 * @brief 注册通用目标检测插件
 *
 * 约定 PluginClass 类必须提供以下公共成员方法：
 *   - zhulong_status_t init(const std::string& config_json);
 *   - zhulong::Session create_session(uint32_t worker_id);
 *   - std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& image);
 */
#define ZHULONG_REGISTER_DETECTOR_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::DetectionResultAdapter)

/**
 * @brief 注册图像分类插件
 *
 * 约定 PluginClass 类必须提供以下公共成员方法：
 *   - zhulong_status_t init(const std::string& config_json);
 *   - zhulong::Session create_session(uint32_t worker_id);
 *   - std::vector<zhulong::Classification> classify(zhulong::Session& session, const zhulong::Image& image);
 */
#define ZHULONG_REGISTER_CLASSIFIER_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::ClassificationResultAdapter)

/**
 * @brief 注册结构化人体/人脸插件
 *
 * 约定 PluginClass 类必须提供以下公共成员方法：
 *   - zhulong_status_t init(const std::string& config_json);
 *   - zhulong::Session create_session(uint32_t worker_id);
 *   - std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image);
 */
#define ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(PluginClass, PluginNameStr, PluginVersionStr) \
    ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER(PluginClass, PluginNameStr, PluginVersionStr, \
                                         ::zhulong::detail::PersonStructureResultAdapter)

#endif /* ZHULONG_ALGO_HPP */
