/**
 * @file person_detector.hpp
 * @brief 基于 SCRFD 2.5G 的人体检测模块
 */

#pragma once

#include "common/crowd_config.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <vector>

namespace zhulong::crowd {

/**
 * @brief 单个检出人体项（归一化坐标 [0, 1]）
 */
struct DetectedPerson {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 严格归一化框 [x1, y1, x2, y2] ∈ [0, 1] */
    float score{0.0f};                                /**< 检测置信度 */
};

/**
 * @brief 单 Worker 独占的前处理工作区（每帧复用，热路径零分配）
 *
 * `letterboxed`（640×384 RGB ≈ 0.7MB）与 `inputs`（NCHW float32 ≈ 2.9MB）若每帧新建，
 * 一路 25fps 就是每秒 ~90MB 的分配抖动。Tensor 的 shape 在构造时固定，因此只能按
 * Worker 预先分配、逐帧覆写；工作区随 Session 生命周期存在，天然满足「一个 Session
 * 只被一个 Worker 线程独占」的契约。
 */
class DetectorWorkspace {
public:
    DetectorWorkspace() = delete;

    /**
     * @brief 按模型输入尺寸分配工作区
     *
     * @throw std::invalid_argument / std::length_error 尺寸非法或分配溢出（由 SDK Tensor 抛出）
     */
    DetectorWorkspace(int model_width, int model_height);

    DetectorWorkspace(const DetectorWorkspace&) = delete;
    DetectorWorkspace& operator=(const DetectorWorkspace&) = delete;
    DetectorWorkspace(DetectorWorkspace&&) = delete;
    DetectorWorkspace& operator=(DetectorWorkspace&&) = delete;
    ~DetectorWorkspace() = default;

    int model_width() const noexcept { return model_width_; }
    int model_height() const noexcept { return model_height_; }

    /** @brief letterbox 输出张量（HWC UINT8），由检测器写入 */
    zhulong::Tensor& letterboxed_tensor() noexcept { return letterboxed_; }
    /** @brief 模型输入张量（NCHW FLOAT32），由检测器写入 */
    zhulong::Tensor& model_input() noexcept { return inputs_.front(); }
    /** @brief 模型前向的输入列表（按 const 引用传给 `Session::forward`，不发生拷贝） */
    const std::vector<zhulong::Tensor>& model_inputs() const noexcept { return inputs_; }

    /** @brief 首次「工作区尺寸与模型不符」返回 true（一个 Worker 只报一次，避免热路径刷屏） */
    bool first_geometry_mismatch() noexcept {
        const bool first = !geometry_mismatch_reported_;
        geometry_mismatch_reported_ = true;
        return first;
    }

    /** @brief 首次「模型输出布局不符」返回 true */
    bool first_layout_mismatch() noexcept {
        const bool first = !layout_mismatch_reported_;
        layout_mismatch_reported_ = true;
        return first;
    }

private:
    int model_width_{0};
    int model_height_{0};
    zhulong::Tensor letterboxed_;         /**< HWC UINT8 letterbox 结果 */
    std::vector<zhulong::Tensor> inputs_; /**< NCHW FLOAT32 模型输入（单元素，按引用传参避免拷贝） */
    bool geometry_mismatch_reported_{false};
    bool layout_mismatch_reported_{false};
};

class PersonDetector {
public:
    PersonDetector() = default;
    PersonDetector(int model_width, int model_height, float nms_threshold = 0.45f) noexcept
        : model_width_(model_width), model_height_(model_height), nms_threshold_(nms_threshold) {}

    /**
     * @brief 调整模型输入分辨率（仅进程级配置生效）
     *
     * 不整除 FPN 最大 stride 的尺寸会让模型输出网格数与解码循环对不上，且**没有错误码**，
     * 只表现为静默零检出 —— 所以这里必须 fail closed，由调用方回退默认值。
     *
     * @return 尺寸合法并已生效返回 true
     */
    bool set_input_size(int model_width, int model_height) noexcept {
        if (!is_valid_detector_input_size(model_width, model_height)) {
            return false;
        }
        model_width_ = model_width;
        model_height_ = model_height;
        return true;
    }

    /**
     * @brief 执行单帧人体检测
     *
     * @param session 当前 Worker 绑定的推理 Session
     * @param image 输入原帧视图
     * @param score_threshold 置信度阈值初筛
     * @param workspace 当前 Worker 独占的前处理工作区（尺寸必须与模型输入一致）
     * @return 过滤并经过 NMS 后的归一化人体列表
     */
    std::vector<DetectedPerson> detect(zhulong::Session& session,
                                       const zhulong::Image& image,
                                       float score_threshold,
                                       DetectorWorkspace& workspace) const;

    int model_width() const noexcept { return model_width_; }
    int model_height() const noexcept { return model_height_; }

private:
    int model_width_{kDefaultDetectInputWidth};
    int model_height_{kDefaultDetectInputHeight};
    float nms_threshold_{0.45f};
};

} // namespace zhulong::crowd
