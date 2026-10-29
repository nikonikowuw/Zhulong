#include "plate_recognition.hpp"

#include "plate_format.hpp"

#include <zhulong/algo.hpp>

#include <dlfcn.h>
#include <unistd.h>

#include <cerrno>
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <mutex>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

namespace zhulong::plate {

namespace {

/** @brief config.schema.json 声明的默认阈值（schema 与代码必须同步） */
constexpr float kDefaultConfidenceThreshold = 0.35f;
constexpr float kDefaultNmsThreshold = 0.45f;

/**
 * @brief 当前进程自身所在目录（不含结尾斜杠）；取不到时返回空串
 *
 * 生产布局里包根可执行文件与 `models/` 恒为同级目录，这是模型解析的权威依据。
 * 主路径读 `/proc/self/exe`（与动态链接器无关，包被替换后路径仍可信），
 * `dladdr` 仅作 fallback（macOS / 无 procfs 的平台）。
 */
std::string executable_directory() {
    std::string self_path;
#if defined(__linux__)
    char buffer[4096];
    const ssize_t length = ::readlink("/proc/self/exe", buffer, sizeof(buffer) - 1);
    if (length > 0) {
        buffer[length] = '\0';
        self_path = buffer;
        // 二进制被原地替换/删除时内核会附加 " (deleted)"，去掉它才指向真实目录。
        constexpr const char* kDeletedSuffix = " (deleted)";
        const std::string suffix = kDeletedSuffix;
        if (self_path.size() > suffix.size() &&
            self_path.compare(self_path.size() - suffix.size(), suffix.size(), suffix) == 0) {
            self_path.erase(self_path.size() - suffix.size());
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
 * 解析顺序（前者优先，越靠前越接近生产真相）：
 *   1. **包根可执行文件所在目录** —— 已安装算法包布局（包根可执行文件与 models/ 同级），
 *      生产唯一权威路径；
 *   2. `ZHULONG_PLATE_MODEL_DIR` / `ZHULONG_MODEL_DIR` —— 部署侧显式覆盖；
 *   3. 仓库内源码树候选 —— 仅供开发期直接跑 build 目录时使用；
 *   4. CWD 相对路径 —— 最后兜底。
 *
 * ⚠️ 顺序不可颠倒：把源码树候选放在最前，会让开发机「意外命中仓库权重」而掩盖
 * 部署布局下的路径缺陷（曾经 `dir + "/models/" + rel_path` 拼出 `models/models/…`，
 * 结果安装后 model_create 直接 -4，但仓库根目录下运行却一切正常）。
 */
std::string resolve_model_path(const std::string& rel_path) {
    const std::string module_dir = executable_directory();
    if (!module_dir.empty()) {
        const std::string candidate = module_dir + "/" + rel_path;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    for (const char* env_name : {"ZHULONG_PLATE_MODEL_DIR", "ZHULONG_MODEL_DIR"}) {
        const char* env_dir = std::getenv(env_name);
        if (env_dir != nullptr && env_dir[0] != '\0') {
            const std::string candidate = std::string(env_dir) + "/" + rel_path;
            if (std::ifstream(candidate).good()) {
                return candidate;
            }
        }
    }

    // 开发期兜底：仅在源码树内有效，部署环境不会命中。
    // ⚠️ 只有本 target 自己的目录，没有跨 target 候选、没有架构宏——找错档位的权重
    //    表现为「能加载但结果全错」，比直接失败难查得多。
    for (const char* prefix : {"packages/plate_recognition/onnx/",
                               "../../../packages/plate_recognition/onnx/",
                               "../../packages/plate_recognition/onnx/",
                               "../packages/plate_recognition/onnx/"}) {
        const std::string candidate = std::string(prefix) + rel_path;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    if (std::ifstream(rel_path).good()) {
        return rel_path;
    }
    return rel_path;
}

/**
 * @brief 模型加载失败时的精确状态码
 *
 * 文件存在却加载不出来 = 当前构建不含真实推理能力（典型：test_stub 夹具构建，
 * 它按设计拒绝一切真实模型路径）；文件不存在 = NOT_FOUND。
 */
zhulong_status_t model_unavailable_status(const std::string& resolved_path) {
    return std::ifstream(resolved_path).good() ? ZHULONG_PLUGIN_ERR_NOT_SUPPORTED
                                               : ZHULONG_PLUGIN_ERR_NOT_FOUND;
}

/**
 * @brief 读取扁平 JSON 里的数值字段（只服务 config.schema.json 声明的键）
 *
 * 刻意不引入 JSON 依赖：算法包必须保持零第三方依赖，可被任意宿主加载。
 * 匹配 `"key"` 后取第一个 `:` 之后的数值，非法或缺失时返回 false。
 */
bool json_number_field(const std::string& json, const std::string& key, double& out) {
    const std::string quoted_key = "\"" + key + "\"";
    const auto key_pos = json.find(quoted_key);
    if (key_pos == std::string::npos) {
        return false;
    }
    const auto colon = json.find(':', key_pos + quoted_key.size());
    if (colon == std::string::npos) {
        return false;
    }
    const char* begin = json.c_str() + colon + 1;
    char* end = nullptr;
    errno = 0;
    const double value = std::strtod(begin, &end);
    if (end == begin || errno == ERANGE || !std::isfinite(value)) {
        return false;
    }
    out = value;
    return true;
}

/**
 * @brief 解析单个阈值：config_json 优先，其次同义环境变量，最后 schema 默认值
 *
 * `.env` 注入的环境变量名取自 .trellis/spec/backend/algorithm-guidelines.md §3
 * （CONFIDENCE_THRESHOLD / NMS_THRESHOLD），使「声明了可调参数」与「真的能调」对齐。
 */
float resolve_threshold(const std::string& config_json, const char* config_key, const char* env_key,
                        float default_value) {
    double value = 0.0;
    if (json_number_field(config_json, config_key, value)) {
        // 落到 (0, 1] 之外视为配置错误，回落默认值而不是悄悄改变检测行为
        if (value > 0.0 && value <= 1.0) {
            return static_cast<float>(value);
        }
        return default_value;
    }
    if (const char* env_value = std::getenv(env_key); env_value != nullptr && env_value[0] != '\0') {
        char* end = nullptr;
        errno = 0;
        const double parsed = std::strtod(env_value, &end);
        if (end != env_value && errno != ERANGE && std::isfinite(parsed) && parsed > 0.0 && parsed <= 1.0) {
            return static_cast<float>(parsed);
        }
    }
    return default_value;
}

} // namespace

    zhulong_status_t PlateRecognitionPlugin::init(const std::string& config_json)
    {
        detector_.set_confidence_threshold(
            resolve_threshold(config_json, "confidence_threshold", "CONFIDENCE_THRESHOLD", kDefaultConfidenceThreshold));
        detector_.set_nms_threshold(
            resolve_threshold(config_json, "nms_threshold", "NMS_THRESHOLD", kDefaultNmsThreshold));

        // 本 target 只打包 ONNX 权重：扩展名是常量，不再由编译期后端宏或运行期探测决定。
        const std::string detection_model = "models/plate_detect.onnx";
        const std::string recognition_model = "models/plate_rec_color.onnx";
        const std::string resolved_detection = resolve_model_path(detection_model);
        const std::string resolved_recognition = resolve_model_path(recognition_model);

        det_model_ = zhulong::Model::load(resolved_detection);
        if (!det_model_.valid()) {
            return model_unavailable_status(resolved_detection);
        }
        rec_model_ = zhulong::Model::load(resolved_recognition);
        if (!rec_model_.valid()) {
            return model_unavailable_status(resolved_recognition);
        }

        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session PlateRecognitionPlugin::create_session(uint32_t worker_id)
    {
        std::lock_guard<std::mutex> lock(session_mutex_);
        if (!det_model_.valid()) {
            return zhulong::Session();
        }
        zhulong::Session det_session = det_model_.create_session(worker_id);
        if (!det_session.valid()) {
            return zhulong::Session();
        }

        if (rec_model_.valid()) {
            zhulong::Session rec_session = rec_model_.create_session(worker_id);
            if (!rec_session.valid()) {
                // 级联模型会话创建失败时整体失败，严禁静默降级导致假活
                return zhulong::Session();
            }
            rec_sessions_[worker_id] = std::move(rec_session);
        }

        return det_session;
    }

    std::vector<zhulong::VehicleItem> PlateRecognitionPlugin::detect_vehicles(
        zhulong::Session& session, const zhulong::Image& image, zhulong::TaskContext& task_context)
    {
        const uint32_t worker_id = session.worker_id();

        // ⚠️ 指针只在本函数内使用：unordered_map 重哈希不会失效元素引用（节点稳定），
        //    但 Session 本身是 Worker 独占的，绝不可跨线程共享该指针所指对象。
        zhulong::Session* rec_session = nullptr;
        {
            std::lock_guard<std::mutex> lock(session_mutex_);
            const auto it = rec_sessions_.find(worker_id);
            if (it != rec_sessions_.end()) {
                rec_session = &it->second;
            }
        }

        // 1. 车牌检测
        const std::vector<PlateBoxDetection> detections = detector_.detect(session, image);
        if (detections.empty()) {
            return {};
        }

        // 2. 逐块识别并构造结构化输出（退化语义：车身框即车牌框，见 algo.hpp VehicleItem）
        const auto& raw = image.raw_view();
        std::vector<zhulong::VehicleItem> vehicles;
        vehicles.reserve(detections.size());

        for (const auto& detection : detections) {
            RecognizedPlate recognized;
            if (rec_session != nullptr && rec_session->valid()) {
                recognized = recognizer_.recognize(*rec_session, raw, detection.box, detection.class_id);
            }

            // ⚠️ 识别失败（框太糊、字符位不足、CTC 全空白）时**不产出**该车牌：
            //    宿主 internal/record 会为 plates 为空且 track_id==0 的目标按框坐标
            //    生成抓拍去重键，而退化模式没有跟踪 ID，框抖动会让去重键持续变化并
            //    产生抓拍洪泛。宁可少报一块「读不出来的车牌」，也不制造无界证据。
            if (recognized.plate_number.empty()) {
                continue;
            }

            // 号牌结构无条件合法性校验：拦截无法在现实中成立的畸形/噪点字符序列
            const PlateFormatVerdict verdict = validate_plate_number(recognized.plate_number);
            if (!verdict.valid) {
                if (const char* debug_env = std::getenv("ZHULONG_PLATE_DEBUG"); debug_env != nullptr && debug_env[0] == '1') {
                    std::fprintf(stderr, "[plate_recognition] rejected invalid plate '%s' (reason: %s)\n",
                                 recognized.plate_number.c_str(),
                                 plate_format_problem_name(verdict.problem));
                }
                continue;
            }

            zhulong::VehicleItem vehicle;
            vehicle.track_id = 0; // 退化模式：无车辆跟踪
            for (size_t k = 0; k < 4; ++k) {
                vehicle.vehicle_box[k] = detection.box[k];
            }
            // 退化语义下「车」就是车牌框：vehicle_score 取**车牌检测**置信度，
            // 而 plate.score 是字符识别置信度，二者语义不同、不可互换。
            vehicle.vehicle_score = detection.score;
            vehicle.vehicle_type = "unknown";
            vehicle.vehicle_color = "";

            zhulong::PlateItem plate;
            for (size_t k = 0; k < 4; ++k) {
                plate.box[k] = detection.box[k];
            }
            plate.score = recognized.score;
            plate.plate_number = std::move(recognized.plate_number);
            plate.plate_color = std::move(recognized.plate_color);
            plate.plate_type = recognized.plate_type;

            vehicle.plates.push_back(std::move(plate));
            vehicles.push_back(std::move(vehicle));
        }

        return vehicles;
    }

} // namespace zhulong::plate
