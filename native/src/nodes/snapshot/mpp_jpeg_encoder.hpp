/**
 * @file mpp_jpeg_encoder.hpp
 * @brief Rockchip MPP VEPU JPEG 硬件编码器（dlopen 装载，编译期零厂商依赖）
 *
 * 核心设计原则：
 * 1. **动态装载**：MPP 通过 `dlopen("librockchip_mpp.so.1")` 使用，`ZhulongEngine`
 *    不引入任何厂商动态库链接期依赖，x86 主机与交叉构建产物一致。
 *    本文件自带符号表，**不复用** `nodes/decode/mpp_loader`：解码节点的判定
 *    要求「全部符号必须就绪」，若把编码符号混进去，一个缺失的 enc 符号会把
 *    硬件解码一并判为不支持 —— 两个能力的可用性必须彼此独立。
 * 2. **会话池化**：`mpp_init(MPP_CTX_ENC)` 是几十毫秒级操作，绝不可每张图重建。
 *    池按 `(width, height)` 分桶复用，满载时按 LRU 淘汰空闲会话。
 * 3. **上传而非零拷贝**：像素由 CPU 侧一次性写入 `mpp_buffer`（NV12 布局），
 *    再由 VEPU 编码。DMA-BUF 直通（`mpp_buffer_import`）已验证 API 可用，
 *    但像素正确性仍需板端复核，故不在此处启用 —— 见
 *    `.trellis/spec/native/hardware-backends.md` 的 JPEG 小节。
 * 4. **静默错误自检**：`self_test()` 用两张仅亮度不同的合成帧证明「亮度真的进了
 *    编码器」。某些 BSP 上曾观察到「编码成功但恒为纯灰图且不报错」，自检是唯一
 *    能在启动期拦住这类错误的机制。
 */

#pragma once

#include "Zhulong/frame.hpp"
#include "nodes/snapshot/jpeg_encoder.hpp"

#include <cstdint>
#include <memory>
#include <vector>

namespace zhulong {

/**
 * @brief VEPU JPEG 硬件编码器
 *
 * 语义约束：
 *   - 线程安全：`encode_region` 与 `self_test` 可并发调用，内部串行化；
 *   - 失败即回退：任何一步失败都返回 false，由调用方回退到软件编码，
 *     **绝不抛异常、绝不返回半成品 JPEG**；
 *   - 生命周期：进程级单例语义，由 `JpegBackend` 持有；会话池不随单次调用销毁。
 */
class MppJpegEncoder {
public:
    /**
     * @brief 探测并构造编码器
     * @return 探针通过（设备节点 + 库 + 会话 + 编码配置均就绪）时返回实例；
     *         否则返回 nullptr（不抛异常，调用方据此判定硬件不可用）
     */
    static std::unique_ptr<MppJpegEncoder> create();

    ~MppJpegEncoder();
    MppJpegEncoder(const MppJpegEncoder &) = delete;
    MppJpegEncoder &operator=(const MppJpegEncoder &) = delete;
    MppJpegEncoder(MppJpegEncoder &&) = delete;
    MppJpegEncoder &operator=(MppJpegEncoder &&) = delete;

    /**
     * @brief 把 CPU 可见帧的一个矩形编码为完整 JPEG
     *
     * 矩形会按 2 像素对齐（色度下采样要求）并 clamp 到画面内，语义与
     * `extract_yuv_region` 一致，因此两条路径（硬件 / 软件）产出同尺寸图像。
     *
     * @param src 源帧视图（借用，调用期间有效）；支持 NV12 与 YUV420P
     * @param x 裁剪左边界（源图像素坐标）
     * @param y 裁剪上边界（源图像素坐标）
     * @param w 裁剪宽度
     * @param h 裁剪高度
     * @param quality JPEG 质量 [1,100]（内部收敛到 MPP 的 q_factor [1,99]）
     * @param[out] out_jpeg 接收完整 JPEG（含 SOI/EOI）；失败时清空
     * @return true 编码成功；false 表示本帧失败，调用方应回退软件编码
     */
    bool encode_region(const CpuFrameView &src, int x, int y, int w, int h, int quality,
                       std::vector<uint8_t> &out_jpeg);

    /**
     * @brief 直通路径：整帧 DMA-BUF 零拷贝编码
     *
     * 把解码器产出的 DMA-BUF 直接 `import` 成 `MppBuffer` 交给 VEPU，**不经过 CPU**。
     * 仅支持 NV12 且 `frame.dma_fd >= 0`；裁剪框不支持（MPP 没有一致可靠的源偏移
     * 语义），调用方对裁剪走 `encode_region`。
     *
     * 产物必须与 `encode_region` 对同一帧的结果**字节级相同** —— 这是直通路径
     * 「没有读错像素」的唯一低成本证明，由 `ZhulongJpegEncoderTests` 断言。
     *
     * @param frame 解码器帧（借用，调用期间有效；fd 必须在返回前仍然有效）
     * @param quality JPEG 质量 [1,100]
     * @param[out] out_jpeg 接收完整 JPEG；失败时清空
     * @return true 成功；false 表示直通不可用或失败，调用方应回退 `encode_region`
     */
    bool encode_dma_frame(const HardwareFrame &frame, int quality,
                          std::vector<uint8_t> &out_jpeg);

    /**
     * @brief 启动期自检：证明硬件编码器真的在读亮度平面
     *
     * 两张 32x32 合成帧仅 Y 平面不同（16 vs 235），色度恒为 128：
     *   - 正常编码器：两份 JPEG 字节必然不同；
     *   - 「恒定灰图」缺陷：两份字节完全相同（因为都只反映了色度）。
     * 同时校验产物 SOF 段声明的尺寸与请求一致。
     *
     * @return true 自检通过；false 说明该 BSP 的硬编路径不可信，必须回退软件
     */
    bool self_test();

private:
    struct Impl;
    explicit MppJpegEncoder(std::unique_ptr<Impl> impl);

    std::unique_ptr<Impl> impl_;
};

} // namespace zhulong
