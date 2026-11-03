/**
 * @file crowd_gathering.cpp
 * @brief 人员非法聚集检测算法插件核心流水线实现
 */

#include "crowd_gathering.hpp"

#include <cstdlib>
#include <fstream>
#include <iostream>

namespace zhulong::crowd {

namespace {

constexpr std::string_view kModelExtension = ".onnx";

std::string resolve_model_path(const std::string& rel_path) {
    if (std::ifstream(rel_path).good()) {
        return rel_path;
    }

    for (const char* env_name : {"ZHULONG_CROWD_MODEL_DIR", "ZHULONG_MODEL_DIR"}) {
        const char* env_dir = std::getenv(env_name);
        if (env_dir != nullptr && env_dir[0] != '\0') {
            const std::string candidate = std::string(env_dir) + "/" + rel_path;
            if (std::ifstream(candidate).good()) {
                return candidate;
            }
        }
    }

    for (const char* prefix : {"packages/crowd_gathering/onnx/",
                               "../../../packages/crowd_gathering/onnx/",
                               "../../packages/crowd_gathering/onnx/",
                               "../packages/crowd_gathering/onnx/"}) {
        const std::string candidate = std::string(prefix) + rel_path;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }

    return rel_path;
}

} // namespace

CrowdGatheringPlugin::CrowdGatheringPlugin() = default;

zhulong_status_t CrowdGatheringPlugin::init(const std::string& config_json) {
    default_config_ = resolve_crowd_config(config_json, default_config_);

    const std::string model_rel = "models/scrfd_person_2.5g" + std::string(kModelExtension);
    const std::string model_path = resolve_model_path(model_rel);

    if (!std::ifstream(model_path).good()) {
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }

    try {
        model_ = zhulong::Model::load(model_path);
    } catch (...) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    if (!model_.valid()) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }

    return ZHULONG_PLUGIN_OK;
}

zhulong::Session CrowdGatheringPlugin::create_session(uint32_t worker_id) {
    if (!model_.valid()) {
        return {};
    }
    return model_.create_session(worker_id);
}

std::vector<zhulong::Detection> CrowdGatheringPlugin::detect(
    zhulong::Session& session,
    const zhulong::Image& image,
    zhulong::TaskContext& task_context
) {
    if (!session.valid()) {
        return {};
    }

    // 1. 解析任务级动态配置（若未下发则回退到进程级配置）
    const CrowdConfig config = resolve_crowd_config(task_context.params.json, default_config_);

    // 2. SCRFD 人体检测
    const auto persons = detector_.detect(session, image, config.score_threshold);

    // 3. 透视自适应空间距离与并查集聚类
    const auto candidates = SpatialClusterer::cluster(
        persons,
        config.gather_distance_ratio,
        config.gather_count_threshold
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

    // 聚集告警外包大框：触发 Go 宿主 alarms 表入库与特写抓拍
    for (const auto& alm : alarms) {
        zhulong::Detection det{};
        det.box[0] = alm.box[0];
        det.box[1] = alm.box[1];
        det.box[2] = alm.box[2];
        det.box[3] = alm.box[3];
        det.confidence = alm.score;
        det.class_id = 1;
        det.label = "crowd_gathering_alarm";
        results.push_back(det);
    }

    // 基础单人人框（可选开启，用于画框与可视化调试）
    if (config.report_person_boxes) {
        for (const auto& p : persons) {
            zhulong::Detection det{};
            det.box[0] = p.box[0];
            det.box[1] = p.box[1];
            det.box[2] = p.box[2];
            det.box[3] = p.box[3];
            det.confidence = p.score;
            det.class_id = 0;
            det.label = "person";
            results.push_back(det);
        }
    }

    return results;
}

} // namespace zhulong::crowd
