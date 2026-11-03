/**
 * @file crowd_gathering.hpp
 * @brief 人员非法聚集检测算法插件核心流水线
 */

#pragma once

#include "analysis/spatial_clustering.hpp"
#include "analysis/temporal_tracker.hpp"
#include "common/crowd_config.hpp"
#include "detection/person_detector.hpp"

#include <zhulong/algo.hpp>

#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

namespace zhulong::crowd {

class CrowdGatheringPlugin {
public:
    CrowdGatheringPlugin();
    ~CrowdGatheringPlugin() = default;

    /**
     * @brief 插件进程级初始化（加载模型与解析全局配置）
     *
     * @param config_json 进程级启动配置 JSON 串（`--config`，一包一份）
     * @return zhulong_status_t 状态码
     */
    zhulong_status_t init(const std::string& config_json);

    /**
     * @brief 为 Worker 创建绑定的执行 Session，并分配该 Worker 独有的前处理工作区
     *
     * @param worker_id Worker 线程编号
     * @return zhulong::Session 运行时会话；任一环节失败返回非法 session（不得半死运行）
     */
    zhulong::Session create_session(uint32_t worker_id);

    /**
     * @brief 单帧推理核心流水线：人体检测 -> 透视自适应聚类 -> 时序防抖研判
     *
     * @param session 当前线程绑定的 Session
     * @param image 输入原帧视图
     * @param task_context 任务上下文（含 task_id 与 params.json）
     * @return 检测与告警结果列表
     */
    std::vector<zhulong::Detection> detect(
        zhulong::Session& session,
        const zhulong::Image& image,
        zhulong::TaskContext& task_context
    );

    const CrowdConfig& default_config() const noexcept { return default_config_; }

private:
    /**
     * @brief 任务级配置缓存项
     *
     * `params.json` 是逐帧下发的配置原文，但改配置是运维动作（分钟级），逐帧重解析是纯浪费：
     * 每帧要构造 5 个键串 + 若干临时量。缓存原文并与新视图比对，命中即直接复用。
     */
    struct CachedTaskConfig {
        std::string raw;      /**< 上次解析的配置原文 */
        CrowdConfig config{}; /**< 对应解析结果 */
    };

    /** @brief 取当前帧生效配置（按 task_id 缓存，原文变化才重新解析） */
    CrowdConfig config_for(uint64_t task_id, std::string_view task_config_json);

    /** @brief 取（必要时创建）该 Worker 的前处理工作区 */
    DetectorWorkspace& workspace_for(uint32_t worker_id);

    CrowdConfig default_config_{};
    zhulong::Model model_{};
    PersonDetector detector_{};
    TemporalTracker tracker_{};

    std::mutex workspaces_mutex_;
    std::unordered_map<uint32_t, std::unique_ptr<DetectorWorkspace>> workspaces_;

    std::mutex configs_mutex_;
    std::unordered_map<uint64_t, CachedTaskConfig> configs_;
};

} // namespace zhulong::crowd
