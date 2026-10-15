/**
 * @file vision_backend.hpp
 * @brief 视觉前处理后端抽象接口（IVisionBackend）与通用 NMS 声明
 *
 * 核心设计原则：
 * 1. 几何语义单一来源：letterbox 的缩放/补边几何由 letterbox_geometry.hpp 统一计算，
 *    各后端实现（CPU / RGA）必须共用同一套参数语义，不得各自发明。
 * 2. LetterboxParams 为 in-out 参数：调用前提供 target 尺寸与格式，调用后被回填
 *    实际 scale / pad_left / pad_top，供后处理将归一化坐标映射回原图。
 * 3. 编译期后端绑定：具体实现由 CMake 变量 ZHULONG_VISION_BACKEND 选定。
 */

#pragma once

#include <vector>
#include <cstdint>
#include <zhulong/plugin.h>
#include "tensor.hpp"

namespace zhulong {

/**
 * @brief Letterbox（等比缩放 + 居中补边）几何参数
 *
 * ⚠️ 本结构为 **in-out** 参数：target_* 与 target_format 为入参，
 * scale / pad_left / pad_top 为几何回填（出参），后处理必须使用回填值反算原图坐标。
 */
struct LetterboxParams {
    int target_width{640};                                        /**< [in] 目标张量宽度 */
    int target_height{640};                                       /**< [in] 目标张量高度 */
    zhulong_pixel_format_t target_format{ZHULONG_PIXEL_FORMAT_RGB24}; /**< [in] 目标像素格式 */
    float scale{1.0f};                                            /**< [out] 实际等比缩放系数（原图→目标） */
    int pad_left{0};                                              /**< [out] 左侧补边像素数 */
    int pad_top{0};                                               /**< [out] 顶部补边像素数 */
};

/**
 * @brief 视觉前处理后端抽象接口
 */
class IVisionBackend {
public:
    virtual ~IVisionBackend() = default;

    /**
     * @brief 图像 Letterbox 预处理（等比缩放、居中补边、颜色转换）
     *
     * @param src 源帧视图（借用，调用期间有效）
     * @param[out] dst 目标张量（HWC、3 通道、UINT8）
     * @param[in,out] inout_params 几何参数（入参为 target，出参回填 scale/pad）
     * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；NOT_SUPPORTED / INVALID_ARGUMENT 等错误码
     */
    virtual zhulong_status_t letterbox(const zhulong_frame_view& src,
                                       Tensor& dst,
                                       LetterboxParams& inout_params) = 0;

    /**
     * @brief 双线性插值图像缩放；仅支持 packed RGB24/BGR24。
     *
     * @param src 源像素首地址（借用）
     * @param src_w 源宽度（必须 > 0）
     * @param src_h 源高度（必须 > 0）
     * @param src_stride 源行步长（0 表示 packed，即 src_w * 3；负值或不足返回 INVALID_ARGUMENT）
     * @param src_format 源像素格式（非 RGB24/BGR24 返回 NOT_SUPPORTED，且不读取源像素）
     * @param dst 目标像素首地址
     * @param dst_stride 目标行步长（同上契约）
     * @param dst_w 目标宽度
     * @param dst_h 目标高度
     * @param dst_format 目标像素格式（RGB/BGR 顺序不一致时必须交换 R/B 通道）
     * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；NOT_SUPPORTED / INVALID_ARGUMENT 等
     */
    virtual zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                    zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                    int dst_w, int dst_h, zhulong_pixel_format_t dst_format) = 0;
};

/**
 * @brief 排序后逐框抑制的标量 NMS（非极大值抑制），最坏复杂度 O(n^2)。
 *
 * ⚠️ 输入框必须为已归一化坐标 [x1, y1, x2, y2] ∈ [0.0, 1.0]；
 * IoU 阈值语义为「保留 IoU <= iou_threshold 的框」。
 *
 * @param boxes 候选框列表（须已归一化）
 * @param iou_threshold 交并比抑制阈值
 * @return std::vector<zhulong_bbox> 抑制后保留的框（按置信度降序）
 */
std::vector<zhulong_bbox> nms(const std::vector<zhulong_bbox>& boxes, float iou_threshold);

} // namespace zhulong

