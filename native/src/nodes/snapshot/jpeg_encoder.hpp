/**
 * @file jpeg_encoder.hpp
 * @brief 静态 FFmpeg MJPEG 编码器封装（单帧 NV12 / YUV420P → 完整 JPEG 字节流）
 *
 * 核心设计原则：
 * 1. **单帧独立编码**：每个实例封装一次性编码所需的全部状态，编码完即可丢弃。
 *    抓拍是低频事件（默认 3s 冷却），复用 codec context 的收益不抵复杂度。
 * 2. **格式归一**：解码器可能产出 NV12（MPP 硬解）或 YUV420P 三平面（FFmpeg 软解），
 *    两者统一喂给同一个编码器；NV12 在喂入前拆成 YUV420P 平面。
 * 3. **不依赖 swscale**：静态 FFmpeg 以 `--disable-swscale` 构建，因此色彩空间转换
 *    只能手工完成。本文件的 NV12→YUV420P 拆分是纯平面重组，不涉及缩放/滤波。
 * 4. **与硬件解耦**：本文件只认「CPU 可见的平面指针 + 行跨度」，不认识 DMA-BUF。
 *    映射 DMA-BUF 是抓拍节点的职责（见 engine.cpp 的 CpuFrameMapping）。
 */

#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace zhulong {

/**
 * @brief 待编码的一帧 YUV 图像（三平面紧凑排列，无硬件 stride padding）
 */
struct YuvFrame {
    uint32_t width{0};      /**< 图像宽度（像素），必须为偶数 */
    uint32_t height{0};     /**< 图像高度（像素），必须为偶数 */
    std::vector<uint8_t> y; /**< Y 平面，长度 = width * height */
    std::vector<uint8_t> u; /**< U 平面，长度 = (width/2) * (height/2) */
    std::vector<uint8_t> v; /**< V 平面，长度 = (width/2) * (height/2) */
};

/**
 * @brief 由解码器产出帧转换而来的 CPU 可见视图
 *
 * planes/strides 的语义与 zhulong_frame_view 一致：strides[0] 为 0 表示紧凑排列。
 * 两种已知格式：
 *   - FRAME_FORMAT_NV12：planes[0]=Y，planes[1]=UV（交错），stride 对两平面通用；
 *   - FRAME_FORMAT_YUV420P：planes[0]=Y，planes[1]=U，planes[2]=V，各自有 stride。
 */
struct CpuFrameView {
    const uint8_t *planes[4]{nullptr, nullptr, nullptr, nullptr};
    uint32_t strides[4]{0, 0, 0, 0};
    uint32_t vstride{0};   /**< 垂直步长（行数），NV12 定位 UV 偏移必需；0 表示等于 height */
    uint32_t width{0};
    uint32_t height{0};
    uint32_t format{0};    /**< FRAME_FORMAT_NV12 或 FRAME_FORMAT_YUV420P */
};

/**
 * @brief 把 CPU 可见帧的一个矩形区域拷贝为紧凑 YuvFrame
 *
 * 支持的源格式：FRAME_FORMAT_NV12 与 FRAME_FORMAT_YUV420P。
 * 裁剪矩形按 2 像素对齐并 clamp 到画面内（色度下采样要求）。
 * 若矩形退化（对齐后宽或高不足 2），返回 false 而不是抛异常。
 *
 * @param src 源帧视图（借用，调用期间有效）
 * @param x 裁剪左边界（源图像素坐标，可为负）
 * @param y 裁剪上边界（源图像素坐标，可为负）
 * @param w 裁剪宽度
 * @param h 裁剪高度
 * @param[out] out 紧凑三平面输出
 * @return true 提取成功；false 格式不支持或矩形无效
 */
bool extract_yuv_region(const CpuFrameView &src, int x, int y, int w, int h, YuvFrame &out);

/**
 * @brief 把归一化检测框换算为像素矩形并 clamp 到画面内
 *
 * 归一化输入与 zhulong_bbox 契约一致（[0,1] 区间，x1<=x2, y1<=y2）。
 * 输出矩形按 2 像素对齐（向画面内侧取整），保证色度平面偏移正确。
 *
 * @param box 归一化 [x1, y1, x2, y2]
 * @param width 画面宽度
 * @param height 画面高度
 * @param[out] out_x 像素左边界
 * @param[out] out_y 像素上边界
 * @param[out] out_w 像素宽度（对齐后，可能为 0）
 * @param[out] out_h 像素高度（对齐后，可能为 0）
 * @return true 矩形有效（对齐后宽高均 >= 2）；false 退化或坐标含非有限值
 */
bool normalized_box_to_pixels(const float box[4], uint32_t width, uint32_t height,
                              int &out_x, int &out_y, int &out_w, int &out_h);

/**
 * @brief 将紧凑 YUV 帧编码为完整 JPEG 字节流
 *
 * @param frame 待编码图像
 * @param quality JPEG 质量，范围 [1, 100]；调用方负责收敛（本函数不做默认值替换）
 * @param[out] out_jpeg 接收完整 JPEG（含 SOI/EOI）
 * @return true 编码成功；false 编码器不可用（静态 FFmpeg 未编译 mjpeg 编码器）或内部错误
 */
bool encode_jpeg(const YuvFrame &frame, int quality, std::vector<uint8_t> &out_jpeg);

/**
 * @brief 查询静态 FFmpeg 是否提供 MJPEG 编码器
 *
 * 用于把「抓拍能力缺失」与「本次抓拍失败」区分开：前者应显式报
 * Zhulong_ERR_UNSUPPORTED，而不是让调用方误以为是流的问题。
 */
bool jpeg_encoder_available();

} // namespace zhulong
