/**
 * @file face_recognition.hpp
 * @brief 人脸人体结构化算法插件类（本 target 唯一实现）
 *
 * 这里只放**声明**：模型与会话生命周期、推理编排、配置解析都在
 * `face_recognition.cpp`。类必须以完整类型暴露（`PluginApi<PluginClass, Adapter>`
 * 要 `make_unique` 它），但成员函数的定义不必跟着进头文件 —— 包可执行文件、
 * devtest 与 native 测试都链接同一个 `face_recognition.cpp` 编译单元。
 */

#pragma once

#include "detection/detector.hpp"
#include "feature/extractor.hpp"
#include "matching/matcher.hpp"

#include <zhulong/algo.hpp>

#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::face {

class FaceRecognitionPlugin {
public:
    FaceRecognitionPlugin();

    /** @brief 动态调整检测器输入分辨率（默认 CCTV 视频流场景为 640×384） */
    void set_detection_input_size(int width, int height) noexcept {
        detector_.set_input_size(width, height);
        person_detector_.set_input_size(width, height);
    }

    /**
     * @brief 加载模型权重并应用插件配置
     * @param config_json 配置 JSON（见 config.schema.json：threshold / top_k）
     * @return OK；人脸检测或特征模型不可用时返回 NOT_FOUND / NOT_SUPPORTED
     */
    zhulong_status_t init(const std::string& config_json);

    /**
     * @brief 为 Worker 创建级联推理会话
     * @param worker_id 执行线程/Worker 唯一编号
     * @return 主检测器会话；任一级联阶段创建失败时返回非法 session（防降级保护）
     */
    zhulong::Session create_session(uint32_t worker_id);

    /**
     * @brief 人体/人脸结构化推理入口
     *
     * @param session create_session 返回的主检测器会话
     * @param image 输入只读帧视图（仅在本次调用期间有效）
     * @param task_context 本路流的任务上下文（当前无消费字段）
     * @return 按空间关系挂载好的结构化结果；无目标时返回空
     */
    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image,
                                                    zhulong::TaskContext& task_context);

private:
    /**
     * @brief worker_id → 级联会话（人体 / 特征）
     *
     * 存 shared_ptr 而不是 Session 本身：`Session` 是 move-only，`detect_persons`
     * 必须在释放互斥锁之后继续使用会话，而并发的 `create_session` 会插入新条目
     * 触发 rehash/覆盖 —— 裸指针或引用在这种情况下会悬垂。
     */
    using SessionTable = std::unordered_map<uint32_t, std::shared_ptr<zhulong::Session>>;

    void parse_config(const std::string& config_json);

    /** @brief 取本 worker 的会话副本；无对应会话时返回空指针（调用方按缺失处理） */
    std::shared_ptr<zhulong::Session> lookup_session(const SessionTable& table, uint32_t worker_id);

    /** @brief 单张人脸：对齐 → 特征提取 → 1:N 比对；失败返回 false（该人脸被跳过） */
    bool build_face_item(const zhulong::Image& image,
                         const FaceDetection& detection,
                         const std::shared_ptr<const std::vector<zhulong::FaceTemplate>>& gallery,
                         zhulong::Session& rec_session,
                         zhulong::Tensor& rec_input,
                         zhulong::FaceItem& out_face);

    zhulong::Model person_model_;
    zhulong::Model det_model_;
    zhulong::Model rec_model_;
    std::mutex sessions_mu_;
    SessionTable person_sessions_;
    SessionTable rec_sessions_;
    PersonDetector person_detector_;
    FaceDetector detector_;
    FaceFeatureExtractor extractor_;
    Matcher matcher_;
};

/**
 * @brief 宿主直连的单次人脸特征提取上下文
 *
 * 由 `zhulong_face_extract_feature` 使用：懒加载检测/特征模型并缓存会话，
 * 由宿主的 TTL Reaper 通过 `zhulong_face_extract_release` 显式释放。
 * 所有公开方法内部串行化，可在任意线程并发调用。
 */
class FaceExtractorContext {
public:
    static FaceExtractorContext& instance();

    /** @brief 释放模型会话与权重（宿主 TTL 空闲回收 / dlclose 前必须调用） */
    void release();

    zhulong_status_t extract(
        const uint8_t* pixel_data,
        int32_t width,
        int32_t height,
        int32_t stride,
        int32_t format,
        float* out_features,
        uint32_t* feature_dim
    );

private:
    /** @brief 懒加载检测与特征模型（调用方必须已持有 mu_） */
    zhulong_status_t init_locked();

    /**
     * @brief 把输入帧整理成 RGB24 连续帧（BGR24 做通道交换，RGB24 零拷贝借用）
     * @return false 表示 stride/宽度非法
     */
    bool prepare_rgb_frame(const uint8_t* pixel_data, int32_t width, int32_t height,
                           int32_t stride, int32_t format,
                           const uint8_t** out_pixels, int32_t* out_stride);

    std::mutex mu_;
    bool initialized_{false};
    zhulong::Model det_model_;
    zhulong::Model rec_model_;
    zhulong::Session det_session_;
    zhulong::Session rec_session_;
    FaceDetector detector_{kDefaultModelWidth, kDefaultModelHeight};
    FaceFeatureExtractor extractor_;
    std::vector<uint8_t> rgb_scratch_;
};

} // namespace zhulong::face
