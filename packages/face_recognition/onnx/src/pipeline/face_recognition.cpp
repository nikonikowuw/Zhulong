/**
 * @file face_recognition.cpp
 * @brief 本 target 的算法实现编译单元 + 宿主引擎用的人脸特征提取 C ABI 入口
 *
 * 本 TU 负责三件「必须恰好一份」的事：
 *   1. `FaceRecognitionPlugin` / `FaceExtractorContext` 的全部方法定义；
 *   2. 模型路径解析（只在包内使用，故内部链接）；
 *   3. 导出 `zhulong_face_extract_feature` / `zhulong_face_extract_release`
 *      （宿主引擎的 `Zhulong_engine_extract_face_feature` 走这两个符号）。
 */

#include "face_recognition.hpp"

#include "alignment/aligner.hpp"
#include "common/face_association.hpp"
#include "common/match_config.hpp"

#include <dlfcn.h>
#include <unistd.h>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <exception>
#include <fstream>
#include <limits>
#include <string_view>
#include <utility>

namespace zhulong::face {

namespace {

/** @brief 本 target 只打包一种权重扩展名；必须与 SDK 推理后端一致（见文件末尾 static_assert） */
constexpr std::string_view kModelExtension = ".onnx";
/** @brief `/proc/self/exe` 读取缓冲上限（PATH_MAX 在跨平台场景里不可靠，取下界更保险） */
constexpr size_t kMaxExecutablePathLength = 4096;
/** @brief 二进制被原地替换/删除时内核会附加该后缀 */
constexpr std::string_view kDeletedSuffix = " (deleted)";

/** @brief 可提取特征的最低人脸置信度（低于它按「没有可用人脸」处理） */
constexpr float kMinimumEmbeddableFaceScore = 0.50f;
/** @brief 宿主特征提取用的固定会话 worker id（与推理 Worker 的命名空间隔离） */
constexpr uint32_t kExtractSessionWorkerID = 999;

/** @brief 已提完特征、待序列化的人脸 */
struct ProcessedFace {
    std::array<float, 4> face_box{0.0f, 0.0f, 0.0f, 0.0f};
    zhulong::FaceItem item;
};

/**
 * @brief 当前进程自身所在目录（不含结尾斜杠）；取不到时返回空串
 *
 * 已安装算法包布局：包根可执行文件与 `models/` 恒为同级目录。
 * 主路径读 `/proc/self/exe`（与动态链接器无关，包被替换后路径仍可信），
 * `dladdr` 只作 fallback（macOS / 无 procfs 的平台）。
 */
std::string executable_directory() {
    std::string self_path;
#if defined(__linux__)
    std::array<char, kMaxExecutablePathLength> buffer{};
    const ssize_t length = ::readlink("/proc/self/exe", buffer.data(), buffer.size() - 1);
    if (length > 0) {
        buffer[static_cast<size_t>(length)] = '\0';
        self_path = buffer.data();
        if (self_path.size() > kDeletedSuffix.size() &&
            self_path.compare(self_path.size() - kDeletedSuffix.size(), kDeletedSuffix.size(), kDeletedSuffix) == 0) {
            self_path.erase(self_path.size() - kDeletedSuffix.size());
        }
    }
#endif
    if (self_path.empty()) {
        Dl_info info;
        if (dladdr(reinterpret_cast<void*>(&executable_directory), &info) == 0 || info.dli_fname == nullptr) {
            return {};
        }
        self_path = info.dli_fname;
    }
    const auto slash = self_path.find_last_of('/');
    if (slash == std::string::npos) {
        return {};
    }
    return self_path.substr(0, slash);
}

/**
 * @brief 解析模型文件路径
 *
 * 顺序：包根（可执行文件同级 `models/`）→ 显式 env 覆盖 → 开发期源码树候选 → CWD。
 * ⚠️ 没有跨 target 候选、没有架构宏：找错档位的权重表现为「能加载但结果全错」，
 *    比直接失败难查得多。
 */
std::string resolve_model_path(std::string_view rel_path) {
    const std::string exe_dir = executable_directory();
    if (!exe_dir.empty()) {
        const std::string candidate = exe_dir + "/" + std::string(rel_path);
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    for (const char* env_name : {"ZHULONG_FACE_MODEL_DIR", "ZHULONG_MODEL_DIR"}) {
        const char* env_dir = std::getenv(env_name);
        if (env_dir != nullptr && env_dir[0] != '\0') {
            const std::string candidate = std::string(env_dir) + "/" + std::string(rel_path);
            if (std::ifstream(candidate).good()) {
                return candidate;
            }
        }
    }

    // 开发期兜底：只在源码树内有效，部署环境不会命中
    for (const char* prefix : {"packages/face_recognition/onnx/",
                               "../../../packages/face_recognition/onnx/",
                               "../../packages/face_recognition/onnx/",
                               "../packages/face_recognition/onnx/"}) {
        const std::string candidate = std::string(prefix) + std::string(rel_path);
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    if (std::ifstream(std::string(rel_path)).good()) {
        return std::string(rel_path);
    }
    return std::string(rel_path);
}

/** @brief 加载一个模型（路径基名 + 本 target 的固定扩展名） */
zhulong::Model load_target_model(std::string_view base_name) {
    return zhulong::Model::load(resolve_model_path(std::string(base_name) + std::string(kModelExtension)));
}

/**
 * @brief 将「模型加载失败」细分为可行动的状态码
 *
 * 文件存在却加载不出来 = 当前构建不含真实推理能力（典型：test_stub 夹具构建，
 * 它按设计拒绝一切真实模型路径）；文件不存在 = NOT_FOUND。
 */
zhulong_status_t model_unavailable_status(std::string_view base_name) {
    const std::string path = resolve_model_path(std::string(base_name) + std::string(kModelExtension));
    return std::ifstream(path).good() ? ZHULONG_PLUGIN_ERR_NOT_SUPPORTED : ZHULONG_PLUGIN_ERR_NOT_FOUND;
}

} // namespace

// 本档位目录只打包一种权重扩展名，且必须与 SDK 推理后端一致：用 onnx 前缀去编
// rk3568 档位（或反之）会「能加载但结果全错」，让它在编译期就失败。
static_assert(std::string_view(zhulong::TARGET_MODEL_EXT) == kModelExtension,
              "本 target 的权重扩展名与 SDK 推理后端不一致：请使用对应后端的 SDK 前缀构建");

// ============================================================================
// FaceRecognitionPlugin
// ============================================================================

FaceRecognitionPlugin::FaceRecognitionPlugin()
    : person_detector_(kCctvStreamModelWidth, kCctvStreamModelHeight),
      detector_(kCctvStreamModelWidth, kCctvStreamModelHeight) {}

zhulong_status_t FaceRecognitionPlugin::init(const std::string& config_json) {
    parse_config(config_json);

    // 1. 人体检测模型 (SCRFD scrfd_person_2.5g) —— 可选，缺失时退化为纯人脸语义
    person_model_ = load_target_model("models/scrfd_person_2.5g");
    // 2. 人脸检测模型 (SCRFD det_500m)
    det_model_ = load_target_model("models/det_500m");
    // 3. 人脸特征提取模型 (FaceLiVTv2-L)
    rec_model_ = load_target_model("models/facelivtv2-l");

    if (!det_model_.valid()) {
        return model_unavailable_status("models/det_500m");
    }
    if (!rec_model_.valid()) {
        return model_unavailable_status("models/facelivtv2-l");
    }
    return ZHULONG_PLUGIN_OK;
}

zhulong::Session FaceRecognitionPlugin::create_session(uint32_t worker_id) {
    zhulong::Session det_session = det_model_.create_session(worker_id);
    zhulong::Session rec_session = rec_model_.create_session(worker_id);

    zhulong::Session person_session;
    if (person_model_.valid()) {
        person_session = person_model_.create_session(worker_id);
        if (!person_session.valid()) {
            return {};
        }
    }

    // 级联会话防降级保护：任一阶段会话创建失败，必须立即中止并返回非法 session
    if (!det_session.valid() || !rec_session.valid()) {
        return {};
    }

    {
        std::lock_guard<std::mutex> lock(sessions_mu_);
        rec_sessions_[worker_id] = std::make_shared<zhulong::Session>(std::move(rec_session));
        if (person_session.valid()) {
            person_sessions_[worker_id] = std::make_shared<zhulong::Session>(std::move(person_session));
        }
    }
    return det_session;
}

std::shared_ptr<zhulong::Session> FaceRecognitionPlugin::lookup_session(const SessionTable& table,
                                                                       uint32_t worker_id) {
    std::lock_guard<std::mutex> lock(sessions_mu_);
    const auto it = table.find(worker_id);
    return it == table.end() ? nullptr : it->second;
}

bool FaceRecognitionPlugin::build_face_item(
    const zhulong::Image& image,
    const FaceDetection& detection,
    const std::shared_ptr<const std::vector<zhulong::FaceTemplate>>& gallery,
    zhulong::Session& rec_session,
    zhulong::Tensor& rec_input,
    float match_threshold,
    size_t match_top_k,
    zhulong::FaceItem& out_face) {
    if (!detection.is_real_detection) {
        return false;
    }

    const auto src_pixels = pixel_landmarks(detection, image.width(), image.height());
    std::array<float, 6> affine_mat{};
    if (!Aligner::estimate_similarity_transform(src_pixels, affine_mat)) {
        return false;
    }
    if (!Aligner::crop_and_align_direct(image, affine_mat, rec_input.data<uint8_t>(),
                                        kFeaturePatchWidth, kFeaturePatchHeight)) {
        return false;
    }

    std::vector<float> probe_features = extractor_.extract(rec_session, rec_input);
    if (probe_features.size() != kFeatureDimension) {
        return false;
    }

    MatchResult match_result = matcher_.match(probe_features.data(), kFeatureDimension, *gallery,
                                              match_threshold, match_top_k);

    out_face.box[0] = detection.face_box[0];
    out_face.box[1] = detection.face_box[1];
    out_face.box[2] = detection.face_box[2];
    out_face.box[3] = detection.face_box[3];
    out_face.quality = detection.face_score;

    out_face.landmarks.clear();
    out_face.landmarks.reserve(kLandmarkCount);
    for (const auto& point : detection.landmarks) {
        out_face.landmarks.push_back({point.x, point.y});
    }

    out_face.matched = match_result.matched;
    out_face.similarity = match_result.similarity;
    out_face.person_id = std::move(match_result.person_id);
    out_face.person_name = std::move(match_result.person_name);
    out_face.matched_face_id = std::move(match_result.matched_face_id);
    out_face.candidates = std::move(match_result.candidates);
    out_face.is_fused = false;
    return true;
}

zhulong::SceneStructure FaceRecognitionPlugin::detect_scene_structure(zhulong::Session& session,
                                                                      const zhulong::Image& image,
                                                                      zhulong::TaskContext& task_context) {
    // 任务级参数（宿主经 MSG_TASK_CONFIG 下发）只影响比对决策：每帧解析一次，
    // 越界值回落进程级默认（见 common/match_config.hpp 的 resolve_match_params）。
    const MatchParams match_params = resolve_match_params(
        task_context.params.json, MatchParams{default_match_threshold_, default_match_top_k_});

    const uint32_t worker_id = session.worker_id();
    const std::shared_ptr<zhulong::Session> rec_session = lookup_session(rec_sessions_, worker_id);
    const std::shared_ptr<zhulong::Session> person_session = lookup_session(person_sessions_, worker_id);

    // 1. 人体目标检测 (SCRFD Person 2.5G)；无人体会话 = 该包未装人体模型，走纯人脸语义
    std::vector<PersonBodyDetection> person_detections;
    if (person_session && person_session->valid()) {
        person_detections = person_detector_.detect(*person_session, image);
        // 只承认真实神经网络检出：降级桩不得生成人体目标（见插件契约 §2）
        person_detections.erase(
            std::remove_if(person_detections.begin(), person_detections.end(),
                           [](const PersonBodyDetection& detection) { return !detection.is_real_detection; }),
            person_detections.end());
    }

    // 2. 人脸检测与 5 点关键点定位 (SCRFD det_500m)
    const std::vector<FaceDetection> face_detections = detector_.detect(session, image);
    if (person_detections.empty() && face_detections.empty()) {
        return {};
    }

    // 3. 每张人脸：仿射对齐、特征向量与底库比对（一帧只取一次底库快照）
    std::vector<ProcessedFace> processed_faces;
    processed_faces.reserve(face_detections.size());
    if (rec_session && rec_session->valid()) {
        const auto gallery = zhulong::GalleryRegistry::instance().snapshot();
        zhulong::Tensor rec_input({1, kFeaturePatchHeight, kFeaturePatchWidth, 3}, zhulong::DataType::UINT8);
        for (const auto& detection : face_detections) {
            ProcessedFace processed;
            processed.face_box = detection.face_box;
            if (build_face_item(image, detection, gallery, *rec_session, rec_input,
                                match_params.threshold, match_params.top_k, processed.item)) {
                processed_faces.push_back(std::move(processed));
            }
        }
    }

    zhulong::SceneStructure scene;
    scene.persons.reserve(person_detections.size());
    scene.faces.reserve(processed_faces.size());

    // 4. 真人体构造（face_count 恒为 0，faces 恒为空）
    for (const auto& person_detection : person_detections) {
        zhulong::PersonItem person;
        person.track_id = 0;
        person.body_box[0] = person_detection.body_box[0];
        person.body_box[1] = person_detection.body_box[1];
        person.body_box[2] = person_detection.body_box[2];
        person.body_box[3] = person_detection.body_box[3];
        person.body_score = person_detection.score;
        scene.persons.push_back(std::move(person));
    }

    // 5. 独立人脸构造与空间几何关联（person_index: -1 为孤立人脸）
    std::vector<bool> person_taken(scene.persons.size(), false);
    for (auto& processed : processed_faces) {
        zhulong::FaceStructureItem face;
        face.box[0] = processed.item.box[0];
        face.box[1] = processed.item.box[1];
        face.box[2] = processed.item.box[2];
        face.box[3] = processed.item.box[3];
        face.quality = processed.item.quality;
        face.landmarks = std::move(processed.item.landmarks);
        face.matched = processed.item.matched;
        face.similarity = processed.item.similarity;
        face.is_fused = processed.item.is_fused;
        face.person_id = std::move(processed.item.person_id);
        face.person_name = std::move(processed.item.person_name);
        face.matched_face_id = std::move(processed.item.matched_face_id);
        face.candidates = std::move(processed.item.candidates);
        face.person_index = -1;

        for (size_t p_idx = 0; p_idx < person_detections.size(); ++p_idx) {
            if (face_belongs_to_person(processed.face_box, person_detections[p_idx].body_box)) {
                face.person_index = static_cast<int32_t>(p_idx);
                person_taken[p_idx] = true;
                break;
            }
        }
        scene.faces.push_back(std::move(face));
    }

    return scene;
}

std::vector<zhulong::PersonItem> FaceRecognitionPlugin::detect_persons(zhulong::Session& session,
                                                                      const zhulong::Image& image,
                                                                      zhulong::TaskContext& task_context) {
    auto scene = detect_scene_structure(session, image, task_context);
    std::vector<zhulong::PersonItem> results = std::move(scene.persons);

    for (auto& f : scene.faces) {
        zhulong::FaceItem fi;
        fi.box[0] = f.box[0];
        fi.box[1] = f.box[1];
        fi.box[2] = f.box[2];
        fi.box[3] = f.box[3];
        fi.quality = f.quality;
        fi.landmarks = std::move(f.landmarks);
        fi.matched = f.matched;
        fi.similarity = f.similarity;
        fi.is_fused = f.is_fused;
        fi.person_id = std::move(f.person_id);
        fi.person_name = std::move(f.person_name);
        fi.matched_face_id = std::move(f.matched_face_id);
        fi.candidates = std::move(f.candidates);

        if (f.person_index >= 0 && static_cast<size_t>(f.person_index) < results.size()) {
            results[static_cast<size_t>(f.person_index)].faces.push_back(std::move(fi));
        } else {
            zhulong::PersonItem degen;
            degen.track_id = 0;
            degen.body_box[0] = fi.box[0];
            degen.body_box[1] = fi.box[1];
            degen.body_box[2] = fi.box[2];
            degen.body_box[3] = fi.box[3];
            degen.body_score = fi.quality;
            degen.faces.push_back(std::move(fi));
            results.push_back(std::move(degen));
        }
    }
    return results;
}

void FaceRecognitionPlugin::parse_config(const std::string& config_json) {
    // 字段与取值范围见 config.schema.json（threshold ∈ [0,1]、top_k ∈ [1,20]）。
    // 越界值保持默认：宿主可能下发老版本配置，宁可退化也不能让插件拒绝启动。
    double threshold = 0.0;
    if (find_json_number(config_json, "threshold", threshold) && threshold >= 0.0 && threshold <= 1.0) {
        default_match_threshold_ = static_cast<float>(threshold);
    }

    double top_k = 0.0;
    if (find_json_number(config_json, "top_k", top_k) && top_k >= 1.0 &&
        top_k <= static_cast<double>(kMaxMatchTopK)) {
        default_match_top_k_ = static_cast<size_t>(std::lround(top_k));
    }
}

// ============================================================================
// FaceExtractorContext：宿主 --extract-feature 的特征提取能力
// ============================================================================

FaceExtractorContext& FaceExtractorContext::instance() {
    static FaceExtractorContext context;
    return context;
}

void FaceExtractorContext::release() {
    std::lock_guard<std::mutex> lock(mu_);
    if (!initialized_) {
        return;
    }
    det_session_ = zhulong::Session();
    rec_session_ = zhulong::Session();
    det_model_ = zhulong::Model();
    rec_model_ = zhulong::Model();
    // TTL 回收的意义就是把常驻内存还回去，缓冲也一并释放
    std::vector<uint8_t>().swap(rgb_scratch_);
    initialized_ = false;
}

bool FaceExtractorContext::prepare_rgb_frame(const uint8_t* pixel_data, int32_t width, int32_t height,
                                             int32_t stride, int32_t format,
                                             const uint8_t** out_pixels, int32_t* out_stride) {
    const int64_t min_stride = static_cast<int64_t>(width) * 3;
    if (min_stride > std::numeric_limits<int32_t>::max()) {
        // 宽度大到 stride 无法用 int32 表达：不猜，直接拒绝
        return false;
    }
    const int32_t effective_stride = (stride == 0) ? static_cast<int32_t>(min_stride) : stride;
    if (effective_stride < min_stride) {
        return false;
    }

    if (format == ZHULONG_PIXEL_FORMAT_RGB24) {
        *out_pixels = pixel_data;
        *out_stride = effective_stride;
        return true;
    }

    // BGR24 → RGB24：复用成员缓冲（本函数只在 mu_ 保护下调用），避免每帧重新分配整帧
    const size_t bytes = static_cast<size_t>(effective_stride) * static_cast<size_t>(height);
    rgb_scratch_.resize(bytes);
    for (int32_t y = 0; y < height; ++y) {
        const uint8_t* src_row = pixel_data + static_cast<size_t>(y) * static_cast<size_t>(effective_stride);
        uint8_t* dst_row = rgb_scratch_.data() + static_cast<size_t>(y) * static_cast<size_t>(effective_stride);
        for (int32_t x = 0; x < width; ++x) {
            const size_t offset = static_cast<size_t>(x) * 3;
            dst_row[offset + 0] = src_row[offset + 2]; // R
            dst_row[offset + 1] = src_row[offset + 1]; // G
            dst_row[offset + 2] = src_row[offset + 0]; // B
        }
    }
    *out_pixels = rgb_scratch_.data();
    *out_stride = effective_stride;
    return true;
}

zhulong_status_t FaceExtractorContext::init_locked() {
    if (initialized_) {
        return ZHULONG_PLUGIN_OK;
    }

    // 1. 人脸检测模型 (SCRFD det_500m)
    det_model_ = load_target_model("models/det_500m");
    // 2. 人脸特征提取模型 (FaceLiVTv2-L)
    rec_model_ = load_target_model("models/facelivtv2-l");

    // 模型缺失属于「能力不可用」，绝不等同于「图中没有人脸」：必须闭环报错，禁止伪造特征。
    if (!det_model_.valid()) {
        return model_unavailable_status("models/det_500m");
    }
    if (!rec_model_.valid()) {
        return model_unavailable_status("models/facelivtv2-l");
    }

    det_session_ = det_model_.create_session(kExtractSessionWorkerID);
    rec_session_ = rec_model_.create_session(kExtractSessionWorkerID);
    if (!det_session_.valid() || !rec_session_.valid()) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }

    initialized_ = true;
    return ZHULONG_PLUGIN_OK;
}

zhulong_status_t FaceExtractorContext::extract(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
) {
    if (pixel_data == nullptr || width <= 0 || height <= 0 || out_features == nullptr ||
        feature_dim == nullptr || *feature_dim < kFeatureDimension) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (format != ZHULONG_PIXEL_FORMAT_BGR24 && format != ZHULONG_PIXEL_FORMAT_RGB24) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    std::lock_guard<std::mutex> lock(mu_);
    if (init_locked() != ZHULONG_PLUGIN_OK) {
        // 模型不可用与「图里没有人脸」必须可区分：前者是包/环境坏了（宿主应报
        // MODEL_UNAVAILABLE 并让运维去查模型），后者是输入问题（NO_FACE_DETECTED）。
        // ⚠️ `init_locked` 在权重缺失时返回 NOT_FOUND，本协议把它理解成「这张脸没检出
        //    人脸」——所以在这一条边界上必须显式折叠成 NOT_SUPPORTED，把包缺陷留在
        //    包侧语义里，绝不伪装成用户照片问题。
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    const uint8_t* rgb_pixels = nullptr;
    int32_t rgb_stride = 0;
    if (!prepare_rgb_frame(pixel_data, width, height, stride, format, &rgb_pixels, &rgb_stride)) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    zhulong_frame_view view{};
    view.width = width;
    view.height = height;
    view.format = ZHULONG_PIXEL_FORMAT_RGB24;
    view.planes[0] = rgb_pixels;
    view.strides[0] = rgb_stride;
    view.dma_buf_fd = -1;

    zhulong::Image image(view);

    // 1. 人脸检测与 5 点关键点定位
    const std::vector<FaceDetection> detections = detector_.detect(det_session_, image);
    if (detections.empty()) {
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }

    // 2. 选取置信度最高的主人脸
    const FaceDetection* best_detection = &detections.front();
    for (const auto& detection : detections) {
        if (detection.face_score > best_detection->face_score) {
            best_detection = &detection;
        }
    }

    // 检出结果来自 CPU 降级桩而非真实神经网络时，说明当前推理后端不具备真实人脸能力：
    // 必须显式返回 NOT_SUPPORTED（闭环报错），严禁用桩坐标伪造出「看起来有效」的特征向量。
    if (!best_detection->is_real_detection) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }
    if (best_detection->face_score < kMinimumEmbeddableFaceScore) {
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }

    // 3. 关键点仿射对齐
    const auto src_pixels = pixel_landmarks(*best_detection, width, height);
    std::array<float, 6> affine_mat{};
    if (!Aligner::estimate_similarity_transform(src_pixels, affine_mat)) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }

    zhulong::Tensor rec_input({1, kFeaturePatchHeight, kFeaturePatchWidth, 3}, zhulong::DataType::UINT8);
    if (!Aligner::crop_and_align_direct(image, affine_mat, rec_input.data<uint8_t>(),
                                        kFeaturePatchWidth, kFeaturePatchHeight)) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }

    const std::vector<float> features = extractor_.extract(rec_session_, rec_input);
    if (features.size() != kFeatureDimension) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }

    // 特征抽取器已保证返回 512 维 L2 归一化单位向量
    std::memcpy(out_features, features.data(), kFeatureDimension * sizeof(float));
    *feature_dim = static_cast<uint32_t>(kFeatureDimension);
    return ZHULONG_PLUGIN_OK;
}

} // namespace zhulong::face

extern "C" {

ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_face_extract_feature(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
) {
    try {
        return zhulong::face::FaceExtractorContext::instance().extract(
            pixel_data, width, height, stride, format, out_features, feature_dim
        );
    } catch (...) {
        // 异常防爆门：任何 C++ 异常都不得穿越 C ABI
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

ZHULONG_PLUGIN_EXPORT void zhulong_face_extract_release() {
    try {
        zhulong::face::FaceExtractorContext::instance().release();
    } catch (...) {
        // 释放接口没有返回值：异常到这里就是终点，吞掉是唯一正确选择
    }
}

} // extern "C"
