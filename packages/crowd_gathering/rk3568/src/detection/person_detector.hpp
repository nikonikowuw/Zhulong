/**
 * @file person_detector.hpp
 * @brief 基于 SCRFD 2.5G 的人体检测模块（rk3568 / RKNN 档位）
 */

#pragma once

#include "common/crowd_config.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <vector>

namespace zhulong::crowd {

/**
 * @brief 本档位 `.rknn` 权重的静态输入尺寸
 *
 * ONNX 档位的 SCRFD 是动态输入（`[1,3,?,?]`），运行期可以按 `--config` 改分辨率；
 * RKNN 权重在转换时就把输入固定成具体 shape（`[1,3,384,640]`），RKNN 后端又按
 * `element_count` 校验输入，尺寸不符时 `forward` 直接失败。所以这里只接受这一组
 * 尺寸：改尺寸 = 重新转换权重，不是改配置。
 */
inline constexpr int kStaticModelWidth = 640;
inline constexpr int kStaticModelHeight = 384;

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
 * RKNN 权重自带 mean/std 归一化，模型直接吃 letterbox 后的 HWC uint8 RGB：工作区里
 * 只有这一块 `[1, 384, 640, 3]` 的输入张量（≈720KB），不再需要 ONNX 档位那份
 * NCHW float32 副本（≈2.9MB）与 256 项归一化查表。Tensor 的 shape 在构造时固定，因此
 * 按 Worker 预先分配、逐帧覆写；工作区随 Session 生命周期存在，天然满足「一个
 * Session 只被一个 Worker 线程独占」的契约。
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

    /** @brief 模型输入张量（NHWC UINT8），letterbox 直接写入这一块，前向不再拷贝 */
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
    std::vector<zhulong::Tensor> inputs_; /**< NHWC UINT8 模型输入（单元素，按引用传参避免拷贝） */
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
     * 本档位的 `.rknn` 是静态 shape，只接受转换时固定的 `kStaticModelWidth ×
     * kStaticModelHeight`：RKNN 后端按 `element_count` 校验输入，其它尺寸会让
     * `forward` 失败——而失败的表现是「静默零检出」，所以这里必须 fail closed，
     * 由调用方（`init()`）映射成启动失败，而不是带着错尺寸跑起来。
     *
     * @return 尺寸合法并已生效返回 true
     */
    bool set_input_size(int model_width, int model_height) noexcept {
        if (!is_valid_detector_input_size(model_width, model_height)) {
            return false;
        }
#if defined(ZHULONG_INFERENCE_RKNN)
        if (model_width != kStaticModelWidth || model_height != kStaticModelHeight) {
            return false;
        }
#endif
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
