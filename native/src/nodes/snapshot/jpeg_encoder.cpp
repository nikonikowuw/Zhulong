/**
 * @file jpeg_encoder.cpp
 * @brief 静态 FFmpeg MJPEG 编码器实现
 *
 * ⚠️ 构建前提：静态 FFmpeg 必须以 `--enable-encoder=mjpeg` 配置构建。
 * 若未启用，jpeg_encoder_available() 返回 false，所有抓拍显式报 UNSUPPORTED，
 * 绝不静默回退到任何软件 JPEG 库（那会引入 GPL/LGPL 之外的第三方依赖）。
 */

#include "nodes/snapshot/jpeg_encoder.hpp"
#include "Zhulong/frame.hpp"

#include <algorithm>
#include <cmath>
#include <cstring>

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/pixfmt.h>
}

namespace zhulong {
namespace {

/** @brief 归一化坐标护栏：拒绝 NaN / Inf，避免 (int)(NaN * w) 的未定义行为 */
bool finite_box(const float box[4]) {
    for (int i = 0; i < 4; ++i) {
        if (!std::isfinite(box[i])) {
            return false;
        }
    }
    return true;
}

/** @brief 向下取偶数（向内对齐，绝不越出有效画面） */
int floor_even(int value) {
    return value < 0 ? 0 : (value & ~1);
}

/** @brief 向上取偶数 */
int ceil_even(int value) {
    return value <= 0 ? 0 : ((value + 1) & ~1);
}

/**
 * @brief 逐行拷贝一个平面（处理源 stride 与目标紧凑排列的差异）
 */
void copy_plane_strided(uint8_t *dst, const uint8_t *src, uint32_t dst_w, uint32_t dst_h,
                        size_t src_stride, int src_x) {
    const uint8_t *row = src + static_cast<size_t>(src_x);
    for (uint32_t y = 0; y < dst_h; ++y) {
        std::memcpy(dst + static_cast<size_t>(y) * dst_w, row, dst_w);
        row += src_stride;
    }
}

/**
 * @brief 从 NV12 交错 UV 平面中抽取单个色度平面（U 或 V）
 *
 * NV12 的 UV 交错布局为 UVUVUV...，抽取时按 2 字节步进取第 channel 字节。
 */
void copy_plane_from_nv12(uint8_t *dst, const uint8_t *uv, uint32_t dst_w, uint32_t dst_h,
                          size_t src_stride, int src_x, int channel) {
    const uint8_t *row = uv + static_cast<size_t>(src_x);
    for (uint32_t y = 0; y < dst_h; ++y) {
        const uint8_t *src = row + static_cast<size_t>(channel);
        uint8_t *out = dst + static_cast<size_t>(y) * dst_w;
        for (uint32_t x = 0; x < dst_w; ++x) {
            out[x] = src[x * 2];
        }
        row += src_stride;
    }
}

} // namespace

bool extract_yuv_region(const CpuFrameView &src, int x, int y, int w, int h, YuvFrame &out) {
    if (src.width == 0 || src.height == 0 || !src.planes[0] || w <= 0 || h <= 0) {
        return false;
    }
    if (src.format != FRAME_FORMAT_NV12 && src.format != FRAME_FORMAT_YUV420P) {
        return false;
    }

    // 1. 裁剪矩形 clamp 并 2 像素对齐（色度下采样要求）
    int x0 = floor_even(x < 0 ? 0 : x);
    int y0 = floor_even(y < 0 ? 0 : y);
    int x1 = ceil_even(x + w);
    int y1 = ceil_even(y + h);
    if (x1 > static_cast<int>(src.width)) x1 = floor_even(static_cast<int>(src.width));
    if (y1 > static_cast<int>(src.height)) y1 = floor_even(static_cast<int>(src.height));
    if (x1 <= x0 || y1 <= y0) {
        return false;
    }
    const uint32_t dw = static_cast<uint32_t>(x1 - x0);
    const uint32_t dh = static_cast<uint32_t>(y1 - y0);
    if (dw < 2 || dh < 2) {
        return false;
    }

    // 2. 源平面 stride：strides[0] == 0 表示紧凑排列
    const size_t y_stride = src.strides[0] != 0 ? src.strides[0] : src.width;

    // 3. 分配紧凑三平面
    out.width = dw;
    out.height = dh;
    out.y.assign(static_cast<size_t>(dw) * dh, 0);
    out.u.assign(static_cast<size_t>(dw / 2) * (dh / 2), 0);
    out.v.assign(static_cast<size_t>(dw / 2) * (dh / 2), 0);

    // 4. Y 平面
    copy_plane_strided(out.y.data(), src.planes[0] + y_stride * static_cast<size_t>(y0), dw, dh,
                       y_stride, x0);

    // 5. 色度平面
    if (src.format == FRAME_FORMAT_YUV420P) {
        // 三平面：U/V 各自独立，stride 通常为 Y 的一半
        if (!src.planes[1] || !src.planes[2]) {
            return false;
        }
        const size_t u_stride = src.strides[1] != 0 ? src.strides[1] : (src.width / 2);
        const size_t v_stride = src.strides[2] != 0 ? src.strides[2] : (src.width / 2);
        const int cx = x0 / 2;
        const int cy = y0 / 2;
        copy_plane_strided(out.u.data(), src.planes[1] + u_stride * static_cast<size_t>(cy),
                           dw / 2, dh / 2, u_stride, cx);
        copy_plane_strided(out.v.data(), src.planes[2] + v_stride * static_cast<size_t>(cy),
                           dw / 2, dh / 2, v_stride, cx);
    } else {
        // NV12：UV 交错且共用 Y 的 stride，UV 起点为 y_stride * vstride
        if (!src.planes[1]) {
            return false;
        }
        const uint8_t *uv_base = src.planes[1];
        const int cx = x0;      // 交错平面里一个色度样本占 2 字节，x 偏移即像素偏移
        const int cy = y0 / 2;
        copy_plane_from_nv12(out.u.data(), uv_base + y_stride * static_cast<size_t>(cy),
                             dw / 2, dh / 2, y_stride, cx, 0);
        copy_plane_from_nv12(out.v.data(), uv_base + y_stride * static_cast<size_t>(cy),
                             dw / 2, dh / 2, y_stride, cx, 1);
    }
    return true;
}

bool normalized_box_to_pixels(const float box[4], uint32_t width, uint32_t height,
                              int &out_x, int &out_y, int &out_w, int &out_h) {
    out_x = 0;
    out_y = 0;
    out_w = 0;
    out_h = 0;
    if (!finite_box(box) || width == 0 || height == 0) {
        return false;
    }

    // 1. 先把坐标 clamp 进 [0,1]：检测框理论上已归一化，但不同算法包可能越界 1e-6
    float x1 = box[0] < 0.0f ? 0.0f : (box[0] > 1.0f ? 1.0f : box[0]);
    float y1 = box[1] < 0.0f ? 0.0f : (box[1] > 1.0f ? 1.0f : box[1]);
    float x2 = box[2] < 0.0f ? 0.0f : (box[2] > 1.0f ? 1.0f : box[2]);
    float y2 = box[3] < 0.0f ? 0.0f : (box[3] > 1.0f ? 1.0f : box[3]);
    if (x2 < x1) std::swap(x1, x2);
    if (y2 < y1) std::swap(y1, y2);

    const int px1 = floor_even(static_cast<int>(x1 * static_cast<float>(width)));
    const int py1 = floor_even(static_cast<int>(y1 * static_cast<float>(height)));
    const int px2 = ceil_even(static_cast<int>(x2 * static_cast<float>(width)));
    const int py2 = ceil_even(static_cast<int>(y2 * static_cast<float>(height)));

    const int cw = px2 - px1;
    const int ch = py2 - py1;
    if (cw < 2 || ch < 2) {
        return false;
    }
    out_x = px1;
    out_y = py1;
    out_w = cw;
    out_h = ch;
    return true;
}

bool jpeg_encoder_available() {
    // avcodec_find_encoder 在编码器未编译进静态库时返回 nullptr。
    // 不缓存：静态链接下结果恒定，一次查找的成本可忽略，而缓存会引入额外的同步。
    return avcodec_find_encoder(AV_CODEC_ID_MJPEG) != nullptr;
}

bool encode_jpeg(const YuvFrame &frame, int quality, std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    if (frame.width < 2 || frame.height < 2 || frame.width % 2 != 0 || frame.height % 2 != 0) {
        return false;
    }
    const size_t y_size = static_cast<size_t>(frame.width) * frame.height;
    const size_t c_size = static_cast<size_t>(frame.width / 2) * (frame.height / 2);
    if (frame.y.size() != y_size || frame.u.size() != c_size || frame.v.size() != c_size) {
        return false;
    }

    const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_MJPEG);
    if (!codec) {
        return false;
    }

    AVCodecContext *ctx = avcodec_alloc_context3(codec);
    if (!ctx) {
        return false;
    }

    // 统一用 YUVJ420P（JPEG 全范围），避免编码器做有限的 range 推断。
    // 上游 NV12 来自硬件解码，其 YUV 范围信息在 HardwareFrame 中未携带，
    // 因此这里按常见约定取全范围；若将来需要精确范围，应在 HardwareFrame 增字段。
    ctx->pix_fmt = AV_PIX_FMT_YUVJ420P;
    ctx->width = static_cast<int>(frame.width);
    ctx->height = static_cast<int>(frame.height);
    ctx->time_base = AVRational{1, 25};
    ctx->color_range = AVCOL_RANGE_JPEG;
    const int clamped_quality = quality < 1 ? 1 : (quality > 100 ? 100 : quality);
    ctx->flags |= AV_CODEC_FLAG_QSCALE;
    ctx->global_quality = FF_QP2LAMBDA * (100 - clamped_quality) / 5;

    if (avcodec_open2(ctx, codec, nullptr) < 0) {
        avcodec_free_context(&ctx);
        return false;
    }

    AVFrame *av_frame = av_frame_alloc();
    if (!av_frame) {
        avcodec_free_context(&ctx);
        return false;
    }
    av_frame->format = ctx->pix_fmt;
    av_frame->width = ctx->width;
    av_frame->height = ctx->height;
    av_frame->color_range = AVCOL_RANGE_JPEG;
    av_frame->quality = ctx->global_quality;

    // 使用 FFmpeg 自己的对齐分配，而不是把我们的 vector 指针挂上去：
    // avcodec 可能按 SIMD 要求访问 padding，挂裸指针会越界读。
    if (av_frame_get_buffer(av_frame, 32) < 0) {
        av_frame_free(&av_frame);
        avcodec_free_context(&ctx);
        return false;
    }

    // 逐行拷贝到 AVFrame（AVFrame 的 linesize 可能大于 width）
    const size_t dst_y_stride = static_cast<size_t>(av_frame->linesize[0]);
    const size_t dst_c_stride = static_cast<size_t>(av_frame->linesize[1]);
    for (uint32_t row = 0; row < frame.height; ++row) {
        std::memcpy(av_frame->data[0] + row * dst_y_stride,
                    frame.y.data() + static_cast<size_t>(row) * frame.width, frame.width);
    }
    for (uint32_t row = 0; row < frame.height / 2; ++row) {
        const size_t src_off = static_cast<size_t>(row) * (frame.width / 2);
        std::memcpy(av_frame->data[1] + row * dst_c_stride, frame.u.data() + src_off, frame.width / 2);
        std::memcpy(av_frame->data[2] + row * dst_c_stride, frame.v.data() + src_off, frame.width / 2);
    }

    bool ok = false;
    if (avcodec_send_frame(ctx, av_frame) >= 0) {
        AVPacket *pkt = av_packet_alloc();
        if (pkt) {
            // MJPEG 是帧内编码：送一帧必然产出一包，无需 flush 循环
            if (avcodec_receive_packet(ctx, pkt) >= 0) {
                if (pkt->size > 0 && pkt->data) {
                    out_jpeg.assign(pkt->data, pkt->data + pkt->size);
                    ok = !out_jpeg.empty();
                }
            }
            av_packet_free(&pkt);
        }
    }

    av_frame_free(&av_frame);
    avcodec_free_context(&ctx);
    return ok;
}

} // namespace zhulong
