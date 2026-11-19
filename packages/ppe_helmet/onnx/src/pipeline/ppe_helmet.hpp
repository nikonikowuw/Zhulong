/**
 * @file ppe_helmet.hpp
 * @brief 安全帽穿戴检测插件（detection 结果类型）
 *
 * 数据流：letterbox → YOLOv8n 推理 → DFL 解码/按类 NMS → 人头-人体关联 →
 * 时序确认 → `no_helmet` 人体框。输出标签特意选 `no_helmet`：宿主
 * internal/record/service.go 的告警关键词命中它，同时它不在
 * `nonAlarmExactLabels` 里，因此会走告警而不是普通抓拍。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

#include "analysis/violation_tracker.hpp"
#include "common/ppe_config.hpp"
#include "detection/yolov8_ppe_detector.hpp"

namespace zhulong::ppe {

class PpeHelmetPlugin {
public:
    /**
     * @brief 加载模型并解析进程级默认配置
     *
     * @param config_json 进程级 `--config` 原文（可为空）
     */
    zhulong_status_t init(const std::string& config_json);

    /** @brief 为 Worker 创建独立会话并预热前处理工作区 */
    zhulong::Session create_session(uint32_t worker_id);

    /** @brief 单帧检测：返回已确认的违规人员框（可选附带合规人员框） */
    std::vector<zhulong::Detection> detect(zhulong::Session& session,
                                           const zhulong::Image& image,
                                           zhulong::TaskContext& task_context);

private:
    struct CachedTaskConfig {
        std::string raw;
        PpeConfig config;
    };

    PpeConfig config_for(uint64_t task_id, std::string_view raw);
    PpeDetectorWorkspace& workspace_for(uint32_t worker_id);

    PpeConfig default_config_;
    zhulong::Model model_;
    Yolov8PpeDetector detector_;
    ViolationTracker tracker_;

    std::mutex workspaces_mutex_;
    std::unordered_map<uint32_t, std::unique_ptr<PpeDetectorWorkspace>> workspaces_;

    std::mutex configs_mutex_;
    std::unordered_map<uint64_t, CachedTaskConfig> configs_;
};

} // namespace zhulong::ppe
