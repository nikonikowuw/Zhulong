/**
 * @file crowd_gathering.cpp
 * @brief 人员非法聚集检测算法插件核心流水线实现
 */

#include "crowd_gathering.hpp"

#include "common/runner_log.hpp"

#include <dlfcn.h>
#include <unistd.h>

#include <array>
#include <cstdlib>
#include <exception>
#include <fstream>
#include <string_view>

namespace zhulong::crowd {

namespace {

/** @brief 本档位只打包一种权重扩展名 */
constexpr std::string_view kModelExtension = ".onnx";
/** @brief 诊断记录的模块名（宿主只接受 `[A-Za-z0-9_.-]`） */
constexpr std::string_view kModuleName = "crowd_gathering";
/** @brief 告警标签：必须包含 `alarm`，宿主据此走告警链路（`internal/record` 的 `isAlarmLabel`） */
constexpr const char* kAlarmLabel = "crowd_gathering_alarm";
/** @brief 个体人框标签：宿主按普通目标抓拍处理 */
constexpr const char* kPersonLabel = "person";
/** @brief 告警项的类别 ID（与个体人框区分） */
constexpr int32_t kAlarmClassId = 1;
/** @brief 个体人框的类别 ID */
constexpr int32_t kPersonClassId = 0;
/** @brief `/proc/self/exe` 读取缓冲上限（PATH_MAX 在跨平台场景里不可靠，取下界更保险） */
constexpr std::size_t kMaxExecutablePathLength = 4096;
/** @brief 二进制被原地替换/删除时内核会附加该后缀 */
constexpr std::string_view kDeletedSuffix = " (deleted)";

/**
 * @brief 当前进程自身所在目录（不含结尾斜杠）；取不到时返回空串
 *
 * 已安装算法包布局：包根可执行文件与 `models/` 恒为同级目录。宿主运行期启动器
 * （`internal/algorunner/process.go`）**不设** `cmd.Dir`，子进程继承的是宿主 CWD ——
 * 所以「按 CWD 找模型」在装机后必然落空，只有可执行文件目录是可信锚点。
 */
std::string executable_directory() {
    std::string self_path;
#if defined(__linux__)
    std::array<char, kMaxExecutablePathLength> buffer{};
    const ssize_t length = ::readlink("/proc/self/exe", buffer.data(), buffer.size() - 1);
    if (length > 0) {
        buffer[static_cast<std::size_t>(length)] = '\0';
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
 * ⚠️ 首位必须是可执行文件目录：验包（`--verify`）与 trial 由宿主以包目录为 CWD 启动，
 *    而运行期 `--broker` 继承宿主 CWD —— 只按 CWD 找会得到「验包绿、装机不启动」。
 */
std::string resolve_model_path(std::string_view rel_path) {
    const std::string relative(rel_path);

    const std::string exe_dir = executable_directory();
    if (!exe_dir.empty()) {
        const std::string candidate = exe_dir + "/" + relative;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    for (const char* env_name : {"ZHULONG_CROWD_MODEL_DIR", "ZHULONG_MODEL_DIR"}) {
        const char* env_dir = std::getenv(env_name);
        if (env_dir != nullptr && env_dir[0] != '\0') {
            const std::string candidate = std::string(env_dir) + "/" + relative;
            if (std::ifstream(candidate).good()) {
                return candidate;
            }
        }
    }

    // 开发期兜底：只在源码树内有效，部署环境不会命中
    for (const char* prefix : {"packages/crowd_gathering/onnx/",
                               "../../../packages/crowd_gathering/onnx/",
                               "../../packages/crowd_gathering/onnx/",
                               "../packages/crowd_gathering/onnx/"}) {
        const std::string candidate = std::string(prefix) + relative;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    return relative;
}

/**
 * @brief 把「模型加载失败」细分为可行动的状态码
 *
 * 文件存在却加载不出来 = 当前构建不含真实推理能力（典型：`test_stub` 夹具后端按设计
 * 拒绝一切真实模型路径）；文件不存在 = NOT_FOUND。两者对运维的动作完全不同。
 */
zhulong_status_t model_unavailable_status(const std::string& model_path) {
    return std::ifstream(model_path).good() ? ZHULONG_PLUGIN_ERR_NOT_SUPPORTED : ZHULONG_PLUGIN_ERR_NOT_FOUND;
}

} // namespace

// 本档位目录只打包一种权重扩展名，且必须与 SDK 推理后端一致：用 onnx 前缀去编
// rknn 档位（或反之）会「能加载但结果全错」，让它在编译期就失败。
static_assert(std::string_view(zhulong::TARGET_MODEL_EXT) == kModelExtension,
              "本 target 的权重扩展名与 SDK 推理后端不一致：请使用对应后端的 SDK 前缀构建");

CrowdGatheringPlugin::CrowdGatheringPlugin() = default;

zhulong_status_t CrowdGatheringPlugin::init(const std::string& config_json) {
    default_config_ = resolve_crowd_config(config_json, CrowdConfig{});

    // 检测输入分辨率是**进程级**参数：模型与工作区尺寸在 Session 创建时固定，
    // 任务级下发不会、也不该改动它。
    const DetectorInputSize input_size = resolve_detector_input_size(config_json, DetectorInputSize{});
    if (input_size.rejected) {
        log_warning(kModuleName, "invalid detect_input_width/height, falling back to defaults",
                    "input_size");
    }
    if (!detector_.set_input_size(input_size.width, input_size.height)) {
        // 校验器与检测器对「合法尺寸」的判断若漂移，宁可启动失败也不要静默零检出
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    const std::string model_path = resolve_model_path("models/scrfd_person_2.5g" + std::string(kModelExtension));
    if (!std::ifstream(model_path).good()) {
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }

    model_ = zhulong::Model::load(model_path);
    if (!model_.valid()) {
        return model_unavailable_status(model_path);
    }

    return ZHULONG_PLUGIN_OK;
}

zhulong::Session CrowdGatheringPlugin::create_session(uint32_t worker_id) {
    if (!model_.valid()) {
        return {};
    }
    zhulong::Session session = model_.create_session(worker_id);
    if (!session.valid()) {
        return {};
    }
    // 工作区与 Session 同寿：预热失败（内存不足）直接让 create_session 失败，
    // 不允许 Worker 跑在「每帧都会分配失败」的半死状态。
    workspace_for(worker_id);
    return session;
}

DetectorWorkspace& CrowdGatheringPlugin::workspace_for(uint32_t worker_id) {
    {
        std::lock_guard<std::mutex> lock(workspaces_mutex_);
        const auto found = workspaces_.find(worker_id);
        if (found != workspaces_.end()) {
            return *found->second;
        }
    }

    // 分配放在锁外：一次 ~6MB，不让其它 Worker 的会话创建排队等它
    auto workspace = std::make_unique<DetectorWorkspace>(detector_.model_width(), detector_.model_height());

    std::lock_guard<std::mutex> lock(workspaces_mutex_);
    // 并发首帧可能同时创建：以先落地者为准，后到的那份直接丢弃
    auto& slot = workspaces_[worker_id];
    if (!slot) {
        slot = std::move(workspace);
    }
    return *slot;
}

CrowdConfig CrowdGatheringPlugin::config_for(uint64_t task_id, std::string_view task_config_json) {
    {
        std::lock_guard<std::mutex> lock(configs_mutex_);
        const auto found = configs_.find(task_id);
        if (found != configs_.end() && std::string_view(found->second.raw) == task_config_json) {
            return found->second.config;
        }
    }

    // 解析会分配若干临时串，放在锁外做
    const CrowdConfig resolved = resolve_crowd_config(task_config_json, default_config_);

    std::lock_guard<std::mutex> lock(configs_mutex_);
    CachedTaskConfig& entry = configs_[task_id];
    entry.raw.assign(task_config_json);
    entry.config = resolved;
    return resolved;
}

std::vector<zhulong::Detection> CrowdGatheringPlugin::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    zhulong::TaskContext& task_context
) {
    const auto& raw = image.raw_view();
    if (!session.valid() || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    // 1. 解析任务级动态配置（按 task_id 缓存，原文未变即复用）
    const CrowdConfig config = config_for(task_context.task_id, task_context.params.json);

    // 2. SCRFD 人体检测（复用该 Worker 的前处理工作区）
    DetectorWorkspace& workspace = workspace_for(session.worker_id());
    const auto persons = detector_.detect(session, image, config.score_threshold, workspace);

    // 3. 透视自适应空间聚类：宽高比决定 X/Y 两轴的相对尺度，必须显式传入
    const float aspect_ratio = static_cast<float>(raw.width) / static_cast<float>(raw.height);
    const auto candidates = SpatialClusterer::cluster(
        persons,
        config.gather_distance_ratio,
        config.gather_count_threshold,
        aspect_ratio
    );

    // 4. 逐路任务时序跟踪与滞留时间防抖
    const auto alarms = tracker_.process(
        task_context.task_id,
        candidates,
        config.gather_duration_seconds
    );

    // 5. 打包输出
    std::vector<zhulong::Detection> results;
    results.reserve(alarms.size() + (config.report_person_boxes ? persons.size() : 0));

    // 聚集告警外包大框：触发 Go 宿主 alarms 表入库与特写抓拍。
    // ⚠️ 检测载荷只有 box/score/label，簇内人数与滞留时长**无法**随结果下发
    //    （`zhulong_bbox` 无 attributes 字段，扩展它需要 ABI v2）。这不是漏填字段，
    //    是当前 ABI 的边界 —— 详见包内 README 的「已知限制」。
    for (const auto& alarm : alarms) {
        zhulong::Detection detection;
        detection.box[0] = alarm.box[0];
        detection.box[1] = alarm.box[1];
        detection.box[2] = alarm.box[2];
        detection.box[3] = alarm.box[3];
        detection.confidence = alarm.score;
        detection.class_id = kAlarmClassId;
        detection.label = kAlarmLabel;
        results.push_back(detection);
    }

    // 基础单人人框（可选开启，用于画框与可视化调试）
    if (config.report_person_boxes) {
        for (const auto& person : persons) {
            zhulong::Detection detection;
            detection.box[0] = person.box[0];
            detection.box[1] = person.box[1];
            detection.box[2] = person.box[2];
            detection.box[3] = person.box[3];
            detection.confidence = person.score;
            detection.class_id = kPersonClassId;
            detection.label = kPersonLabel;
            results.push_back(detection);
        }
    }

    return results;
}

} // namespace zhulong::crowd
