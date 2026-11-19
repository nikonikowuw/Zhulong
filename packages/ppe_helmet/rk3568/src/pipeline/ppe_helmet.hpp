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

#include "analysis/helmet_association.hpp"
#include "analysis/violation_tracker.hpp"
#include "common/ppe_config.hpp"
#include "detection/yolov8_ppe_detector.hpp"

namespace zhulong::ppe {

/**
 * @brief 每 Worker 独占的运行期缓冲
 *
 * `detect` 会被多个 Worker 线程并发调用，因此检测器的张量/解码缓冲与关联输出
 * 都必须按 Worker 隔离，不能挂在插件成员上。
 */
struct PpeWorkerWorkspace {
    PpeDetectorWorkspace detector;
    std::vector<HelmetDecision> decisions;
};

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

    /**
     * @brief 单帧检测：返回已确认的违规人员框（可选附带合规人员框）
     *
     * `report_compliant=true` 时附带输出的人员框**不参与** `confirm_frames` 的时序
     * 确认：只要本帧有头部证据就输出一次，宿主侧「只抓拍不告警」需自行按人去重。
     */
    std::vector<zhulong::Detection> detect(zhulong::Session& session,
                                           const zhulong::Image& image,
                                           zhulong::TaskContext& task_context);

private:
    struct CachedTaskConfig {
        std::string raw;
        PpeConfig config;
    };

    PpeConfig config_for(uint64_t task_id, std::string_view raw);
    PpeWorkerWorkspace& workspace_for(uint32_t worker_id);

    PpeConfig default_config_;
    zhulong::Model model_;
    Yolov8PpeDetector detector_;
    ViolationTracker tracker_;

    std::mutex workspaces_mutex_;
    std::unordered_map<uint32_t, std::unique_ptr<PpeWorkerWorkspace>> workspaces_;

    std::mutex configs_mutex_;
    std::unordered_map<uint64_t, CachedTaskConfig> configs_;
};

} // namespace zhulong::ppe
