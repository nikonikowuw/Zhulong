/**
 * @file yolov8_ppe_decode.hpp
 * @brief DAMO-YOLO-S 安全帽模型输出解码（DFL + 按类 NMS），不依赖任何推理后端
 *
 * 模型为 DAMO-YOLO-S（safety-helmet），输入 512x288（NCHW），三个 FPN 尺度（stride 8/16/32）各一对输出：
 *   reg_{s} [1, 68, H/stride, W/stride]  4 边 × 17 bin（reg_max=16）的 DFL logits
 *   cls_{s} [1,  2, H/stride, W/stride]  2 类概率（模型内**已** sigmoid）
 * 类别 0 为 head_helmet（safety hat），类别 1 为 head_nohelmet（no safety hat）。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstddef>
#include <cstdint>
#include <vector>

namespace zhulong::ppe {

/** @brief 模型类别 ID（0=safety hat, 1=no safety hat） */
inline constexpr int32_t kClassHeadHelmet = 0;
inline constexpr int32_t kClassHeadNoHelmet = 1;
/** @brief 模型类别总数（cls 张量的通道数） */
inline constexpr int32_t kClassCount = 2;
/** @brief DFL 每个方向的分箱数（reg_max + 1 = 17，68 通道 ÷ 4 边） */
inline constexpr int kDflBins = 17;

/** @brief 候选上限：避免异常模型输出把内存顶穿，超出后按置信度截断 */
inline constexpr size_t kPpeMaxCandidates = 2048;
/** @brief 单帧输出上限（按类 NMS 之后）；关联阶段的定长数组按它取尺寸 */
inline constexpr size_t kPpeMaxDetections = 128;

/**
 * @brief 单个 FPN 尺度的输出视图（借用模型输出张量内存，调用期间有效）
 */
struct Yolov8PpeHeadView {
    const float* box{nullptr};   /**< [1,64,grid_h,grid_w] 的起始地址 */
    const float* score{nullptr}; /**< [1,17,grid_h,grid_w] 的起始地址 */
    int grid_h{0};
    int grid_w{0};
    int stride_h{1};
    int stride_w{1};
};

/**
 * @brief 把 letterbox 坐标映射回原图所需的几何参数
 */
struct Yolov8PpeGeometry {
    float scale{1.0f};     /**< letterbox 实际缩放系数 */
    int pad_left{0};       /**< letterbox 左侧补边 */
    int pad_top{0};        /**< letterbox 顶部补边 */
    int image_width{0};    /**< 原始帧宽度 */
    int image_height{0};   /**< 原始帧高度 */
};

/**
 * @brief 解码期间复用的缓冲，由每 Worker 的 workspace 持有
 *
 * 候选、抑制标记与输出三者都跨帧保留容量，热路径因此不再每帧分配三条 vector；
 * 对象不可热插拔，不允许在解码中途被替换。
 */
struct PpeDecodeBuffers {
    std::vector<zhulong::Detection> candidates; /**< DFL 解码的原始候选（scratch） */
    std::vector<zhulong::Detection> detections; /**< 按类 NMS 后的输出 */
    std::vector<uint8_t> suppressed;            /**< NMS 抑制标记 */
};

/**
 * @brief 解码三维输出，按类 NMS 后把结果写入 `buffers.detections`（归一化坐标）
 *
 * @param heads 三个尺度（stride 8/16/32）的输出视图
 * @param geometry letterbox 回映射参数
 * @param score_threshold 置信度阈值（对 sigmoid 后的概率）
 * @param nms_threshold 每类 NMS 的 IoU 阈值
 * @param buffers 复用缓冲；`detections` 在入口被清空，出口持有本次结果
 *
 * 几何非法时不写任何结果（`detections` 保持空）。
 */
void decode_yolov8_ppe(const std::vector<Yolov8PpeHeadView>& heads,
                       const Yolov8PpeGeometry& geometry,
                       float score_threshold,
                       float nms_threshold,
                       PpeDecodeBuffers& buffers);

/** @brief 类别 ID 对应的标签（未知类别返回 "object"） */
const char* ppe_class_label(int32_t class_id) noexcept;

} // namespace zhulong::ppe
