#include "pipeline/ppe_helmet.hpp"

#include "common/runner_log.hpp"

#include <dlfcn.h>
#include <unistd.h>

#include <algorithm>
#include <cstdlib>
#include <cstring>
#include <exception>
#include <filesystem>
#include <string>
#include <system_error>
#include <utility>

namespace zhulong::ppe {
namespace {

constexpr char kModule[] = "ppe_helmet";

std::string executable_directory() {
    char buffer[4096] = {};
    const ssize_t length = ::readlink("/proc/self/exe", buffer, sizeof(buffer) - 1);
    if (length > 0) {
        std::string path(buffer, static_cast<size_t>(length));
        const std::string deleted_suffix = " (deleted)";
        if (path.size() > deleted_suffix.size() &&
            path.compare(path.size() - deleted_suffix.size(), deleted_suffix.size(),
                         deleted_suffix) == 0) {
            path.resize(path.size() - deleted_suffix.size());
        }
        return std::filesystem::path(path).parent_path().string();
    }

    Dl_info info{};
    if (::dladdr(reinterpret_cast<void*>(&executable_directory), &info) != 0 &&
        info.dli_fname != nullptr) {
        return std::filesystem::path(info.dli_fname).parent_path().string();
    }
    return {};
}

/**
 * @brief 按「可执行文件同级 models/ → env → 源码树 → 相对路径」解析权重
 *
 * 装机后可执行文件与 models/ 同目录，第一顺位就是唯一可信锚点；源码树前缀只为
 * 开发期 `build/*_devtest` 不设 env 时兜底，不参与装机运行。
 */
std::string resolve_model_path(const std::string& relative) {
    std::vector<std::string> candidates;
    const std::string exe_dir = executable_directory();
    if (!exe_dir.empty()) {
        candidates.push_back(exe_dir + "/" + relative);
    }
    for (const char* env_name : {"ZHULONG_PPE_MODEL_DIR", "ZHULONG_MODEL_DIR"}) {
        const char* base = std::getenv(env_name);
        if (base != nullptr && base[0] != '\0') {
            candidates.push_back(std::string(base) + "/" + relative);
        }
    }
    for (const char* prefix : {"packages/ppe_helmet/onnx/", "packages/ppe_helmet/rk3568/",
                               "../../../packages/ppe_helmet/onnx/",
                               "../../../packages/ppe_helmet/rk3568/",
                               "../../../../packages/ppe_helmet/onnx/",
                               "../../../../packages/ppe_helmet/rk3568/"}) {
        candidates.push_back(std::string(prefix) + relative);
    }
    candidates.push_back(relative);

    for (const std::string& candidate : candidates) {
        std::error_code error;
        if (std::filesystem::is_regular_file(candidate, error)) {
            return candidate;
        }
    }
    return relative;
}

void copy_box(float* destination, const float* source) noexcept {
    destination[0] = source[0];
    destination[1] = source[1];
    destination[2] = source[2];
    destination[3] = source[3];
}

} // namespace

zhulong_status_t PpeHelmetPlugin::init(const std::string& config_json) {
    default_config_ = resolve_ppe_config(config_json, PpeConfig{});

    const std::string relative = "models/ppe_helmet" + std::string(TARGET_MODEL_EXT);
    const std::string model_path = resolve_model_path(relative);
    std::error_code error;
    if (!std::filesystem::is_regular_file(model_path, error)) {
        log_warning(kModule, "model not found", model_path);
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }
    model_ = zhulong::Model::load(model_path);
    if (!model_.valid()) {
        log_warning(kModule, "model load failed", model_path);
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }
    return ZHULONG_PLUGIN_OK;
}

zhulong::Session PpeHelmetPlugin::create_session(uint32_t worker_id) {
    zhulong::Session session = model_.create_session(worker_id);
    if (!session.valid()) {
        return session;
    }
    // 工作区分配失败就不能让会话半死运行：后续 detect 没有前处理缓冲可用。
    try {
        (void)workspace_for(worker_id);
    } catch (const std::exception& error) {
        log_warning(kModule, "workspace allocation failed", error.what());
        return zhulong::Session();
    }
    return session;
}

std::vector<zhulong::Detection> PpeHelmetPlugin::detect(zhulong::Session& session,
                                                        const zhulong::Image& image,
                                                        zhulong::TaskContext& task_context) {
    if (!session.valid()) {
        return {};
    }
    const auto& raw = image.raw_view();
    if (raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    const PpeConfig config = config_for(task_context.task_id, task_context.params.json);
    PpeDetectorWorkspace& workspace = workspace_for(session.worker_id());
    const std::vector<zhulong::Detection> detections =
        detector_.detect(session, image, config.score_threshold, config.nms_threshold, workspace);
    const std::vector<HelmetDecision> decisions =
        associate_helmets(detections, config.min_person_height_ratio);
    std::vector<zhulong::Detection> results =
        tracker_.confirm(task_context.task_id, decisions, config.confirm_frames);

    if (config.report_compliant) {
        for (const HelmetDecision& decision : decisions) {
            if (decision.violation) {
                continue;
            }
            zhulong::Detection detection;
            copy_box(detection.box, decision.box);
            detection.confidence = std::min(decision.person_score, decision.head_score);
            detection.class_id = kClassHeadHelmet;
            detection.label = "helmet";
            results.push_back(std::move(detection));
        }
    }

    return results;
}

PpeConfig PpeHelmetPlugin::config_for(uint64_t task_id, std::string_view raw) {
    std::lock_guard<std::mutex> lock(configs_mutex_);
    const auto it = configs_.find(task_id);
    if (it != configs_.end() && it->second.raw == raw) {
        return it->second.config;
    }
    CachedTaskConfig cached;
    cached.config = resolve_ppe_config(raw, default_config_);
    cached.raw.assign(raw.data(), raw.size());
    configs_[task_id] = std::move(cached);
    return configs_[task_id].config;
}

PpeDetectorWorkspace& PpeHelmetPlugin::workspace_for(uint32_t worker_id) {
    std::lock_guard<std::mutex> lock(workspaces_mutex_);
    std::unique_ptr<PpeDetectorWorkspace>& workspace = workspaces_[worker_id];
    if (!workspace) {
        workspace = std::make_unique<PpeDetectorWorkspace>();
    }
    return *workspace;
}

} // namespace zhulong::ppe
