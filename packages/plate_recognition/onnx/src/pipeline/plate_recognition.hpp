/**
 * @file plate_recognition.hpp
 * @brief 车牌检测 + 字符/颜色识别插件类（本 target 唯一实现）
 *
 * 资源模型：检测模型会话由 Worker 直接持有（`create_session` 的返回值）；识别模型会话
 * 按 worker_id 保存，供同一 Worker 在执行 `detect_vehicles` 时取用。
 */

#pragma once

#include "detection/plate_detector.hpp"
#include "recognition/plate_recognizer.hpp"

#include <zhulong/algo.hpp>

#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::plate {

/**
 * @brief 车牌结构化算法插件（一进程一个实例，多 Worker 并发调用）
 *
 * 线程模型：`create_session` 在 worker 启动期调用一次；`detect_vehicles` 由各 Worker
 * 在自己的线程上并发调用。因此本类只共享两类东西 —— 无状态的 Stage 对象（检测器/
 * 识别器），以及按 worker_id 索引的会话表（`session_mutex_` 保护）。
 */
class PlateRecognitionPlugin {
public:
    PlateRecognitionPlugin() = default;

    /** @brief 加载两个模型并应用配置（阈值见 config.schema.json） */
    zhulong_status_t init(const std::string& config_json);

    /**
     * @brief 为 Worker 创建级联推理会话
     *
     * 任一级联阶段创建失败时返回非法 session：宁可整路失败，也不能让 Worker 跑在
     * 「检测能出框、识别永远空」的半死状态（那只会产出无车牌的结构化垃圾）。
     */
    zhulong::Session create_session(uint32_t worker_id);

    std::vector<zhulong::VehicleItem> detect_vehicles(zhulong::Session& session, const zhulong::Image& image,
                                                      zhulong::TaskContext& task_context);

private:
    PlateDetector detector_;
    PlateRecognizer recognizer_;

    zhulong::Model det_model_;
    zhulong::Model rec_model_;

    /**
     * @brief worker_id → 识别会话
     *
     * ⚠️ 用 `shared_ptr` 而不是裸 `Session`：worker 重建（重连/热更新）时同一个
     * worker_id 会被重新赋值，值语义的 `Session` 一旦被替换就会在「另一个线程正
     * 拿着它推理」的同时析构。节点稳定性只保证重哈希不失效元素引用，不保证同键覆盖。
     *
     * ⚠️ 且**只增不减**：SDK 的 worker_destroy 不回调插件，没有「该 worker 走了」的
     * 信号。进程生命周期内 worker 数量 == 相机流数量，因此不做回收；若将来宿主会
     * 用单调递增的 worker_id 反复建/销 worker，这里必须先拿到释放钩子再谈回收。
     */
    std::unordered_map<uint32_t, std::shared_ptr<zhulong::Session>> rec_sessions_;
    std::mutex session_mutex_;
};

} // namespace zhulong::plate
