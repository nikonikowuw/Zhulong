/**
 * @file yuv_sampling.hpp
 * @brief YUV（NV12 / YUV420P）双线性采样与 BT.601 → RGB 转换（后端无关）
 *
 * 核心设计原则：只做算术，不分配内存、不依赖任何后端。
 *
 * 提取理由：同一个「YUV 半平面采样 + BT.601 矩阵」在 CPU 视觉前处理后端与车牌识别
 * 插件的裁剪采样里各写了一份。两份实现一旦在 UV 索引、clamp 边界或系数上漂移，
 * 同一条码流在「喂给模型」和「裁给识别器」两条路径上就会得到不同颜色，
 * 而这类偏差在真机上只表现为「识别率莫名变低」，极难归因。
 */

#pragma once

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>

namespace zhulong {

/**
 * @brief 单通道平面双线性采样
 *
 * 坐标为浮点像素中心语义；越界一律 clamp 到边缘，不外扩、不返回 0。
 */
inline float sample_plane_bilinear(const uint8_t* plane, int width, int height, int stride,
                                   float fx, float fy) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(width - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(height - 1));
    const int x0 = static_cast<int>(std::floor(fx));
    const int y0 = static_cast<int>(std::floor(fy));
    const int x1 = std::min(x0 + 1, width - 1);
    const int y1 = std::min(y0 + 1, height - 1);
    const float dx = fx - x0;
    const float dy = fy - y0;
    const uint8_t* row0 = plane + static_cast<size_t>(y0) * stride;
    const uint8_t* row1 = plane + static_cast<size_t>(y1) * stride;
    const float top = row0[x0] * (1.0f - dx) + row0[x1] * dx;
    const float bottom = row1[x0] * (1.0f - dx) + row1[x1] * dx;
    return top * (1.0f - dy) + bottom * dy;
}

/**
 * @brief NV12 交错 UV 平面双线性采样
 *
 * 半平面内每个采样点占 2 字节（U、V 相邻），x 坐标因此需要 ×2 换算到字节偏移。
 */
inline void sample_nv12_uv_bilinear(const uint8_t* plane, int uv_width, int uv_height, int stride,
                                    float fx, float fy, float& out_u, float& out_v) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(uv_width - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(uv_height - 1));
    const int x0 = static_cast<int>(std::floor(fx));
    const int y0 = static_cast<int>(std::floor(fy));
    const int x1 = std::min(x0 + 1, uv_width - 1);
    const int y1 = std::min(y0 + 1, uv_height - 1);
    const float dx = fx - x0;
    const float dy = fy - y0;
    const uint8_t* row0 = plane + static_cast<size_t>(y0) * stride;
    const uint8_t* row1 = plane + static_cast<size_t>(y1) * stride;

    const float u_top = row0[x0 * 2] * (1.0f - dx) + row0[x1 * 2] * dx;
    const float u_bottom = row1[x0 * 2] * (1.0f - dx) + row1[x1 * 2] * dx;
    out_u = u_top * (1.0f - dy) + u_bottom * dy;

    const float v_top = row0[x0 * 2 + 1] * (1.0f - dx) + row0[x1 * 2 + 1] * dx;
    const float v_bottom = row1[x0 * 2 + 1] * (1.0f - dx) + row1[x1 * 2 + 1] * dx;
    out_v = v_top * (1.0f - dy) + v_bottom * dy;
}

/**
 * @brief ITU-R BT.601 有限范围 YUV → RGB（浮点，clamp 到 [0, 255]）
 *
 * ⚠️ 使用 BT.601 有限范围（YUV 取值 16~235，UV 以 128 为中性）系数。
 * 若换成 BT.709 或全范围系数，颜色会整体偏冷/偏亮，且与训练侧预处理不再一致。
 */
inline void yuv_to_rgb(float y, float u, float v, float& out_r, float& out_g, float& out_b) {
    out_r = std::clamp(y + 1.402f * (v - 128.0f), 0.0f, 255.0f);
    out_g = std::clamp(y - 0.344136f * (u - 128.0f) - 0.714136f * (v - 128.0f), 0.0f, 255.0f);
    out_b = std::clamp(y + 1.772f * (u - 128.0f), 0.0f, 255.0f);
}

/**
 * @brief 同上，但输出四舍五入的 8 位通道（用于 packed RGB24/BGR24 落盘）
 */
inline void yuv_to_rgb_u8(float y, float u, float v, uint8_t& out_r, uint8_t& out_g, uint8_t& out_b) {
    float r = 0.0f;
    float g = 0.0f;
    float b = 0.0f;
    yuv_to_rgb(y, u, v, r, g, b);
    out_r = static_cast<uint8_t>(std::lround(r));
    out_g = static_cast<uint8_t>(std::lround(g));
    out_b = static_cast<uint8_t>(std::lround(b));
}

} // namespace zhulong
