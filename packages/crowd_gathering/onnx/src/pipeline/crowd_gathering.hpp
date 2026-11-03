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

#include <string>
#include <vector>

namespace zhulong::crowd {

class CrowdGatheringPlugin {
public:
    CrowdGatheringPlugin();
    ~CrowdGatheringPlugin() = default;

    /**
     * @brief 插件进程级初始化（加载模型与解析全局配置）
     *
     * @param config_json 进程级启动配置 JSON 串
     * @return zhulong_status_t 状态码
     */
    zhulong_status_t init(const std::string& config_json);

    /**
     * @brief 为 Worker 创建绑定的执行 Session
     *
     * @param worker_id Worker 线程编号
     * @return zhulong::Session 运行时会话
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
    CrowdConfig default_config_{};
    zhulong::Model model_{};
    PersonDetector detector_{640, 640};
    TemporalTracker tracker_{};
};

} // namespace zhulong::crowd
