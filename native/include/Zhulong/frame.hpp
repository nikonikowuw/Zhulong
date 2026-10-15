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
 * @brief 常用像素格式常量 (与 Linux DRM FourCC 格式码兼容定义)
 */
constexpr uint32_t FRAME_FORMAT_UNKNOWN = 0;           /**< 未知或未指定格式 */
constexpr uint32_t FRAME_FORMAT_YUV420P = 0x32315559; /**< 'YU12' / I420 三平面连续 YUV420P 格式 */
constexpr uint32_t FRAME_FORMAT_NV12    = 0x3231564E; /**< 'NV12' 两平面 Y 平面 + UV 交错平面格式 */

/**
 * @brief 专为异构多算法推理管线设计的统一硬件帧对象
 *
 * 封装解码器产出的图像缓冲，包含几何跨度、异构显存/DMA句柄、时序元数据与 RAII 回收器。
 * 强制通过 std::shared_ptr<const HardwareFrame> 进行跨算法节点只读共享，禁用拷贝。
 */
struct HardwareFrame {
    using Ptr = std::shared_ptr<HardwareFrame>;
    using ConstPtr = std::shared_ptr<const HardwareFrame>;

    // =========================================================================
    // 1. 几何与跨度 (硬件前处理 RGA / VPC / CUDA 寄存器配置必须字段)
    // =========================================================================
    uint32_t width{0};                     /**< 图像有效显示宽度 (像素数) */
    uint32_t height{0};                    /**< 图像有效显示高度 (像素数) */
    uint32_t stride{0};                    /**< 水平行跨度 (字节数, Width Stride / Pitch)，含硬件对齐 padding */
    uint32_t vstride{0};                   /**< 垂直对齐步长 (行数, Height Stride)，关键：用于准确定位 NV12 UV 偏移；为 0 时表示紧凑排列（等于 height） */
    uint32_t format{FRAME_FORMAT_UNKNOWN}; /**< 像素格式 (如 FRAME_FORMAT_YUV420P / FRAME_FORMAT_NV12) */

    // =========================================================================
    // 2. 异构物理内存句柄 (视具体平台驱动填充，平台互斥或仅部分有效)
    // =========================================================================
    int      dma_fd{-1};                   /**< DMA-BUF 文件描述符 (Rockchip MPP / Jetson / V4L2)，未启用时为 -1 */
    void*    dev_ptr{nullptr};             /**< 异构设备显存指针 (华为昇腾 DVPP acldvppMalloc / CUDA devPtr)，非设备内存时为 nullptr */
    uint8_t* host_ptr{nullptr};            /**< 单平面/打包格式的 CPU 虚拟地址 (仅软解/调试回退有效) */
    uint8_t* host_planes[4]{nullptr, nullptr, nullptr, nullptr}; /**< 各平面独立的 CPU 虚拟地址 (多平面软解时使用，如 Y/U/V 各自独立) */
    uint32_t plane_strides[4]{0, 0, 0, 0}; /**< 各平面独立的水平行跨度 (字节数) */

    // =========================================================================
    // 3. 关联事件与时序
    // =========================================================================
    int64_t  pts{0};                       /**< 原生时间戳 (源流时间基下的滴答数，回传推理结果事件时保持对齐) */
    bool     has_pts{false};               /**< 是否存在有效 PTS (⚠️ 缺失 PTS 不得被当作有效时间戳 0) */
    int32_t  time_base_num{1};             /**< 来源流时间基分子 (Timebase Numerator) */
    int32_t  time_base_den{90000};         /**< 来源流时间基分母 (Timebase Denominator) */
    uint64_t stream_id{0};                 /**< 所属物理流 ID */

    // =========================================================================
    // 4. 专属回收回调 (帧完全释放后归还解码器缓冲池)
    // =========================================================================
    /**
     * @brief 帧回收回调函数
     *
     * 当最后一个持有的 shared_ptr 析构并调用 HardwareFrame 析构函数时触发，
     * 负责将底层的解码缓冲块或显存槽位安全归还给解码器缓冲池。
     */
    std::function<void(HardwareFrame* self)> release_fn;

    HardwareFrame() = default;

    /**
     * @brief RAII 析构函数，自动触发底层缓冲资源回收
     *
     * 内部全面捕获未知异常，确保析构安全，绝不抛出异常。
     */
    ~HardwareFrame() {
        if (release_fn) {
            try {
                release_fn(this);
            } catch (...) {
                // 析构函数严禁抛出异常
            }
        }
    }

    /**
     * @brief 计算 Y 平面大小与 UV 平面起始偏移量 (针对 NV12 / YUV420SP)
     *
     * @return size_t Y 平面在连续内存中所占的字节总数（stride * vstride 或 stride * height）
     */
    [[nodiscard]] size_t y_plane_size() const noexcept {
        return static_cast<size_t>(stride) * (vstride > 0 ? vstride : height);
    }

    // 禁用拷贝语义，强制通过 shared_ptr 进行只读共享
    HardwareFrame(const HardwareFrame&) = delete;
    HardwareFrame& operator=(const HardwareFrame&) = delete;

    /**
     * @brief 移动构造函数 (安全转移所有权，防二次释放)
     *
     * ⚠️ 转移所有权后必须清空源对象的 release_fn 与句柄，
     * 避免源对象析构时二次触发底层解码器缓冲池回收。
     *
     * @param other 源对象
     */
    HardwareFrame(HardwareFrame&& other) noexcept
        : width(other.width),
          height(other.height),
          stride(other.stride),
          vstride(other.vstride),
          format(other.format),
          dma_fd(other.dma_fd),
          dev_ptr(other.dev_ptr),
          host_ptr(other.host_ptr),
          host_planes{other.host_planes[0], other.host_planes[1], other.host_planes[2], other.host_planes[3]},
          plane_strides{other.plane_strides[0], other.plane_strides[1], other.plane_strides[2], other.plane_strides[3]},
          pts(other.pts),
          has_pts(other.has_pts),
          time_base_num(other.time_base_num),
          time_base_den(other.time_base_den),
          stream_id(other.stream_id),
          release_fn(std::move(other.release_fn)) {
        other.release_fn = nullptr;
        other.dma_fd = -1;
        other.dev_ptr = nullptr;
        other.host_ptr = nullptr;
        for (int i = 0; i < 4; ++i) {
            other.host_planes[i] = nullptr;
            other.plane_strides[i] = 0;
        }
    }

    /**
     * @brief 移动赋值运算符 (安全转移所有权，防二次释放)
     *
     * 移动前若当前对象持有未回收的 release_fn，将先执行安全回收；
     * 随后接管 other 的句柄与 release_fn，并将 other 清空置空以防二次释放。
     *
     * @param other 源对象
     * @return HardwareFrame& 当前对象引用
     */
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
            for (int i = 0; i < 4; ++i) {
                host_planes[i] = other.host_planes[i];
                plane_strides[i] = other.plane_strides[i];
                other.host_planes[i] = nullptr;
                other.plane_strides[i] = 0;
            }
            pts = other.pts;
            has_pts = other.has_pts;
            time_base_num = other.time_base_num;
            time_base_den = other.time_base_den;
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
