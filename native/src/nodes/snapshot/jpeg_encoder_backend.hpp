/**
 * @file jpeg_encoder_backend.hpp
 * @brief JPEG 编码后端选择（硬件 VEPU 优先，软件 FFmpeg 兜底）
 *
 * 核心设计原则：
 * 1. **回退是常态路径，不是异常路径**：硬件不可用（非 Rockchip 平台、BSP 不支持
 *    MJPEG 编码、自检失败）时透明回退软件编码，调用方只看到成功结果。这与
 *    `rga_vision_backend` 的回退原则一致。
 * 2. **进程级单例**：C ABI `Zhulong_stream_snapshot` 没有可携带状态的上下文，
 *    后端选择只能在进程内做一次；`instance()` 是函数局部静态量，初始化天然线程安全，
 *    初始化完成后不再变化（只读）。
 * 3. **运行期开关**：`ZHULONG_JPEG_BACKEND=cpu|mpp|auto`（默认 `auto`）。
 *    未知取值按 `cpu` 处理并记录 ERROR —— fail closed，避免拼写错误把硬件
 *    静默打开。
 */

#pragma once

#include "Zhulong/frame.hpp"
#include "nodes/snapshot/jpeg_encoder.hpp"

#include <atomic>
#include <memory>
#include <vector>

namespace zhulong {

class MppJpegEncoder;

/** @brief 实际生效的编码后端 */
enum class JpegBackendKind {
    kSoftware, /**< 静态 FFmpeg 的 mjpeg 编码器 */
    kMpp,      /**< Rockchip VEPU 硬件编码 */
};

class JpegBackend {
public:
    /** @brief 进程级后端（首次调用触发探测，之后只读） */
    static JpegBackend &instance();

    JpegBackendKind kind() const noexcept {
        return kind_;
    }

    /** @brief 是否启用了硬件编码 */
    bool hardware() const noexcept {
        return hw_ != nullptr;
    }

    /** @brief 是否具备编码能力（任一后端） */
    bool available() const noexcept {
        return hardware() || software_available_;
    }

    /**
     * @brief 给定像素矩形是否应该走硬件
     *
     * 实测（RK3568，见 `.trellis/spec/native/hardware-backends.md`）：小图硬编
     * **不占优** —— 上传拷贝 + ioctl 固定开销（≈0.5ms）超过小面积 DCT 的成本。
     * 128x128（16384 像素）**及以下**软件更快或持平，因此用像素面积做分流。
     *
     * 调度意义：省下的是**共享硬件单元 VEPU 的排队时间**与一次无用的拷贝，
     * 而不是精度或正确性——两条路径产出等价图像。
     */
    bool prefer_hardware(int w, int h) const noexcept {
        if (hw_ == nullptr || w <= 0 || h <= 0) {
            return false;
        }
        return static_cast<long long>(w) * h > kMinPixelsForHardware;
    }

    /**
     * @brief 编码源帧的一个矩形区域：优先硬件，失败或未启用即回退软件
     *
     * 传入矩形必须是已对齐（2 像素）并 clamp 进画面的结果，如
     * `normalized_box_to_pixels` 的产出；两个后端的取整规则一致，
     * 因此产物尺寸不随后端变化。
     *
     * @param[out] out_jpeg 完整 JPEG；失败时清空
     * @return true 成功（由任一后端产出）；false 表示两个后端都失败
     */
    bool encode(const CpuFrameView &src, int x, int y, int w, int h, int quality,
                std::vector<uint8_t> &out_jpeg);

    /**
     * @brief 直通编码：整帧 DMA-BUF 直接交给 VEPU，零 CPU 拷贝
     *
     * 下列任一情况返回 false（调用方回退 `encode`，语义等价）：
     *   - 硬件未启用 / 矩形未达分流阈值 / 非 NV12 / 无 `dma_fd`；
     *   - 直通连续失败达 `kMaxDmaFailures` 后**永久停用**该路径；
     *   - **首次自证失败**（见下）。
     *
     * ⚠️ 首次调用会做一次性自证：对**同一帧**分别用直通与上传路径编码，产物必须
     *    字节级相同。这证明「真实解码器帧的 fd 语义」与 dma_heap 测试缓冲一致 ——
     *    后者能在 CI 里验证，前者只能在真机上验证。失败即永久停用并记 ERROR。
     *
     * ⚠️ 永久停用是防退化的关键：若某 BSP 的 import 语义不兼容，每帧都会
     *    「先付一次 import 开销再回退」，反而比不走直通更慢。
     *
     * @param frame 解码器帧（提供 dma_fd / stride / vstride）
     * @param view 同一帧的 CPU 可见视图，仅用于首次自证与失败回退
     */
    bool encode_dma(const HardwareFrame &frame, const CpuFrameView &view, int w, int h,
                    int quality, std::vector<uint8_t> &out_jpeg);

    static const char *kind_name(JpegBackendKind kind) noexcept;

    /** @brief 硬编分流阈值（像素面积），见 `prefer_hardware` */
    static constexpr long long kMinPixelsForHardware = 128 * 128;

    /** @brief 直通路径连续失败多少次后永久停用 */
    static constexpr int kMaxDmaFailures = 3;

private:
    JpegBackend();
    JpegBackend(const JpegBackend &) = delete;
    JpegBackend &operator=(const JpegBackend &) = delete;

    std::unique_ptr<MppJpegEncoder> hw_;
    JpegBackendKind kind_{JpegBackendKind::kSoftware};
    bool software_available_{false};
    enum class DmaState {
        kUnverified, /**< 尚未用真实解码器帧自证 */
        kValidated,  /**< 已自证与上传路径字节级一致 */
        kDisabled,   /**< 自证失败或连续失败，永久停用 */
    };

    std::atomic<int> dma_failures_{0};
    std::atomic<DmaState> dma_state_{DmaState::kUnverified};
};

} // namespace zhulong
