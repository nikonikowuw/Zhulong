# 技术设计：Native 异构硬件解码帧对象与多算法零拷贝管线

## 1. 核心数据结构设计

### 1.1 头文件路径
定义于 `native/include/Zhulong/frame.hpp`。该头文件作为内部 C++ 节点流水线共享的基础抽象。

```cpp
#pragma once

#include <cstdint>
#include <memory>
#include <functional>

namespace zhulong {

/**
 * @brief 专为异构多算法推理管线设计的统一硬件帧对象
 */
struct HardwareFrame {
    using Ptr = std::shared_ptr<HardwareFrame>;
    using ConstPtr = std::shared_ptr<const HardwareFrame>;

    // 1. 几何与跨度 (用于硬件前处理 RGA / VPC / CUDA 寄存器配置)
    uint32_t width{0};          // 图像有效显示宽度 (像素)
    uint32_t height{0};         // 图像有效显示高度 (像素)
    uint32_t stride{0};         // 水平行跨度 (字节数, Width Stride / Pitch)
    uint32_t vstride{0};        // 垂直对齐步长 (行数, Height Stride, 关键: 用于计算 NV12 UV 偏移)
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
    
    // RAII 析构时触发资源回收
    ~HardwareFrame() {
        if (release_fn) {
            release_fn(this);
        }
    }

    // 计算 Y 平面大小与 UV 平面起始偏移量 (针对 NV12 / YUV420SP)
    [[nodiscard]] size_t y_plane_size() const noexcept {
        return static_cast<size_t>(stride) * (vstride > 0 ? vstride : height);
    }

    // 禁用拷贝语义，强制通过 shared_ptr 进行只读共享
    HardwareFrame(const HardwareFrame&) = delete;
    HardwareFrame& operator=(const HardwareFrame&) = delete;
    HardwareFrame(HardwareFrame&&) noexcept = default;
    HardwareFrame& operator=(HardwareFrame&&) noexcept = default;
};

} // namespace zhulong
```

---

## 2. 异构驱动映射机制

| 平台 | 解码器来源 | 帧字段映射 | 前处理单元 (CSC / Resize) | NPU 输入张量 |
| --- | --- | --- | --- | --- |
| **Rockchip RK3588** | MPP (`MppFrame`) | `dma_fd = mpp_buffer_get_fd(buf)`<br>`stride = mpp_frame_get_hor_stride()`<br>`vstride = mpp_frame_get_ver_stride()` | librga (`rga_buffer_t`) 传入 `dma_fd` | `rknn_create_mem_from_fd` 或 RGA DMA 写入张量缓冲 |
| **华为昇腾 (Ascend)** | DVPP VDEC (`hi_video_frame_info`) | `dev_ptr = frame.v_frame.virt_addr[0]`<br>`stride = frame.v_frame.width_stride[0]`<br>`vstride = frame.v_frame.height_stride[0]` | DVPP VPC (`hi_mpi_vpc_crop_resize`) 传入 `dev_ptr` | Device 显存地址直接绑定 ACL Tensor 缓冲区 |
| **NVIDIA Jetson** | Jetpack (`NvBufSurface`) | `dma_fd = surf->surfaceList[0].bufferDesc`<br>`stride = surf->surfaceList[0].pitch`<br>`vstride = surf->surfaceList[0].height` | CUDA Kernel / VIC 传入 fd 映射的显存 | TensorRT 输入显存指针 |
| **x86 CPU 软解** | FFmpeg (`AVFrame`) | `host_ptr = frame->data[0]`<br>`stride = frame->linesize[0]`<br>`vstride = frame->height` | OpenCV / libswscale / CPU SIMD 转换 | CPU Tensor / OpenVINO / ONNXRuntime 内存 |

---

## 3. 1:N 扇出时序与防死锁回收规范

```txt
[解码器工作线程]
       │
       ├─► 1. 解出硬件帧，创建 std::shared_ptr<HardwareFrame>
       │      绑定 release_fn (还回解码器 Buffer Pool)
       │
       ├─► 2. 分发给 N 个活跃算法的前处理任务 (通过 shared_ptr 增加引用计数: RefCount = N)
       │
[算法 A 线程/协程]                          [算法 B 线程/协程]
       │                                          │
 3a. 提交硬件前处理 (RGA / VPC)              3b. 提交硬件前处理 (RGA / VPC)
     输入: frame->dma_fd                        输入: frame->dma_fd
     输出: Tensor_A 显存                         输出: Tensor_B 显存
     耗时: ~0.5ms (纯 DMA)                      耗时: ~0.5ms (纯 DMA)
       │                                          │
 4a. 前处理完成/提交完成                     4b. 前处理完成/提交完成
     立即 drop 引用: frame.reset()              立即 drop 引用: frame.reset()
     (RefCount 从 2 减为 1)                     (RefCount 从 1 减为 0)
       │                                          │
       │                                     ⚡ RefCount == 0: 立即触发 ~HardwareFrame()
       │                                        调用 release_fn 归还解码器缓冲池！
       ▼                                          ▼
 5a. 独立进行 NPU_A 推理 (耗时 8ms)          5b. 独立进行 NPU_B 推理 (耗时 90ms)
     只访问私有 Tensor_A                        只访问私有 Tensor_B
```

**关键准则**：
- **生命周期解耦**：`HardwareFrame` 的寿命严格限制在「前处理提交/完成阶段」（$\le 1\text{ms}$）。
- **杜绝慢算法反噬**：无论算法 B 的模型多庞大、推理耗时多久，解码器的物理缓冲区已经在第 4 步归还，解码器缓冲池永远不会耗尽！

---

## 4. 关键实现与安全防御

1. **`release_fn` 异常安全性**：
   - 使用 RAII 析构保障异常安全；若析构前发生异常，栈展开会自动释放 `shared_ptr` 并触发 `release_fn`。
   - `release_fn` 内部必须捕获任何底层异常，保证析构函数绝不抛出异常。
2. **移动语义保护**：
   - 支持移动构造与移动赋值；移动后源对象的 `release_fn` 置空，防止二次释放。
