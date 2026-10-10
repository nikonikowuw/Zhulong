/**
 * @file frame.hpp
 * @brief 专为异构多算法推理管线设计的统一硬件帧对象
 *
 * 核心设计原则：
 * 1. 极简高效（MVD）：仅包含硬件前处理（RGA/VPC/CUDA）与模型对齐必须的几何、步长与物理寻址。
 * 2. 1:N 零拷贝共享：通过 std::shared_ptr<HardwareFrame> 实现多算法并发只读共享底层物理句柄。
 * 3. 防死锁生命周期闭环：基于 RAII，在最后一个持有者释放时自动触发 release_fn 归还底层解码器缓冲池。
 */

#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <utility>

namespace zhulong {

/**
 * @brief 专为异构多算法推理管线设计的统一硬件帧对象
 */
struct HardwareFrame {
    using Ptr = std::shared_ptr<HardwareFrame>;
    using ConstPtr = std::shared_ptr<const HardwareFrame>;

    // 1. 几何与跨度 (硬件前处理 RGA / VPC / CUDA 寄存器配置必须字段)
    uint32_t width{0};          // 图像有效显示宽度 (像素)
    uint32_t height{0};         // 图像有效显示高度 (像素)
    uint32_t stride{0};         // 水平行跨度 (字节数, Width Stride / Pitch)
    uint32_t vstride{0};        // 垂直对齐步长 (行数, Height Stride, 关键: 用于准确定位 NV12 UV 偏移)
    uint32_t format{0};         // 像素格式 (如 DRM_FORMAT_NV12)

    // 2. 异构物理内存句柄 (视具体平台驱动填充)
    int      dma_fd{-1};        // DMA-BUF 文件描述符 (Rockchip MPP / Jetson / V4L2)
    void*    dev_ptr{nullptr};  // 异构设备显存指针 (华为昇腾 DVPP acldvppMalloc / CUDA)
    uint8_t* host_ptr{nullptr}; // CPU 虚拟地址 (仅软解/调试回退有效)

    // 3. 关联事件与时序
    int64_t  pts{0};            // 原生时间戳 (回传推理结果事件时保持对齐)
    uint64_t stream_id{0};      // 所属物理流 ID

    // 4. 专属回收回调 (帧完全释放后归还解码器缓冲池)
    std::function<void(HardwareFrame* self)> release_fn;

    HardwareFrame() = default;

    // RAII 析构时触发资源回收，确保异常安全
    ~HardwareFrame() {
        if (release_fn) {
            try {
                release_fn(this);
            } catch (...) {
                // 析构函数严禁抛出异常
            }
        }
    }

    // 计算 Y 平面大小与 UV 平面起始偏移量 (针对 NV12 / YUV420SP)
    [[nodiscard]] size_t y_plane_size() const noexcept {
        return static_cast<size_t>(stride) * (vstride > 0 ? vstride : height);
    }

    // 禁用拷贝语义，强制通过 shared_ptr 进行只读共享
    HardwareFrame(const HardwareFrame&) = delete;
    HardwareFrame& operator=(const HardwareFrame&) = delete;

    // 移动构造函数 (安全转移所有权，清空源对象的 release_fn 以防二次释放)
    HardwareFrame(HardwareFrame&& other) noexcept
        : width(other.width),
          height(other.height),
          stride(other.stride),
          vstride(other.vstride),
          format(other.format),
          dma_fd(other.dma_fd),
          dev_ptr(other.dev_ptr),
          host_ptr(other.host_ptr),
          pts(other.pts),
          stream_id(other.stream_id),
          release_fn(std::move(other.release_fn)) {
        other.release_fn = nullptr;
        other.dma_fd = -1;
        other.dev_ptr = nullptr;
        other.host_ptr = nullptr;
    }

    // 移动赋值运算符
    HardwareFrame& operator=(HardwareFrame&& other) noexcept {
        if (this != &other) {
            if (release_fn) {
                try {
                    release_fn(this);
                } catch (...) {
                }
            }
            width = other.width;
            height = other.height;
            stride = other.stride;
            vstride = other.vstride;
            format = other.format;
            dma_fd = other.dma_fd;
            dev_ptr = other.dev_ptr;
            host_ptr = other.host_ptr;
            pts = other.pts;
            stream_id = other.stream_id;
            release_fn = std::move(other.release_fn);

            other.release_fn = nullptr;
            other.dma_fd = -1;
            other.dev_ptr = nullptr;
            other.host_ptr = nullptr;
        }
        return *this;
    }
};

} // namespace zhulong
