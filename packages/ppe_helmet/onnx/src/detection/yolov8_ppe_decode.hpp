/**
 * @file yolov8_ppe_decode.hpp
 * @brief YOLOv8 安全帽模型输出解码（DFL + 按类 NMS），不依赖任何推理后端
 *
 * 模型为 YOLOv8n，输入 640x384（NCHW），三个 FPN 尺度（stride 8/16/32）各一对输出：
 *   box_{s}   [1, 64, H/stride, W/stride]  4 边 × 16 bin 的 DFL logits
 *   score_{s} [1, 17, H/stride, W/stride]  17 类 logits（模型内**无** sigmoid）
 * 只保留 person / head_helmet / head_nohelmet 三类；NMS 必须按 class_id 分组，
 * 否则 person 框会把框内的头部框抑制掉。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstdint>
#include <vector>

namespace zhulong::ppe {

/** @brief 模型类别 ID（0..16 顺序见 ppe_labels_list.txt） */
inline constexpr int32_t kClassHeadHelmet = 12;
inline constexpr int32_t kClassHeadNoHelmet = 13;
inline constexpr int32_t kClassPerson = 14;
/** @brief 模型类别总数（score 张量的通道数） */
inline constexpr int32_t kClassCount = 17;

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
 * @brief 解码三维输出并返回**按类** NMS 后的检测框（归一化坐标）
 *
 * @param heads 三个尺度（stride 8/16/32）的输出视图
 * @param geometry letterbox 回映射参数
 * @param score_threshold 置信度阈值（对 sigmoid 后的概率）
 * @param nms_threshold 每类 NMS 的 IoU 阈值
 * @param scratch 复用的候选缓冲；函数返回后其内容不再有意义
 * @return 按置信度降序的检测框；几何非法时返回空
 */
std::vector<zhulong::Detection> decode_yolov8_ppe(
    const std::vector<Yolov8PpeHeadView>& heads,
    const Yolov8PpeGeometry& geometry,
    float score_threshold,
    float nms_threshold,
    std::vector<zhulong::Detection>& scratch);

/** @brief 类别 ID 对应的标签（未知类别返回 "object"） */
const char* ppe_class_label(int32_t class_id) noexcept;

} // namespace zhulong::ppe
