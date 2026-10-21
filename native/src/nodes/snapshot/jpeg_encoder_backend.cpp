/**
 * @file jpeg_encoder_backend.cpp
 * @brief JPEG 编码后端选择实现
 */

#include "nodes/snapshot/jpeg_encoder_backend.hpp"

#include "common/log.hpp"
#include "nodes/snapshot/mpp_jpeg_encoder.hpp"

#include <cstdlib>
#include <cstring>

namespace zhulong {
namespace {

constexpr const char *kModule = "nodes.snapshot.jpeg";

/** @brief 后端选择环境变量名 */
constexpr const char *kBackendEnv = "ZHULONG_JPEG_BACKEND";

/** @brief 环境变量取值 */
enum class Requested {
    kAuto,
    kCpu,
    kMpp,
};

Requested parse_requested(const char *raw) {
    if (raw == nullptr || raw[0] == '\0' || std::strcmp(raw, "auto") == 0) {
        return Requested::kAuto;
    }
    if (std::strcmp(raw, "cpu") == 0) {
        return Requested::kCpu;
    }
    if (std::strcmp(raw, "mpp") == 0) {
        return Requested::kMpp;
    }
    // fail closed：未知取值按 cpu 处理，避免拼写错误静默开启硬件路径
    ZL_LOGE(0, kModule, "invalid_jpeg_backend_value: %s", raw);
    return Requested::kCpu;
}

} // namespace

JpegBackend &JpegBackend::instance() {
    static JpegBackend backend;
    return backend;
}

const char *JpegBackend::kind_name(JpegBackendKind kind) noexcept {
    return kind == JpegBackendKind::kMpp ? "mpp" : "software";
}

JpegBackend::JpegBackend() {
    software_available_ = jpeg_encoder_available();
    if (!software_available_) {
        // 软件编码器缺失不是致命错误：只要硬件可用，抓拍依然成立
        ZL_LOGW(0, kModule, "software_jpeg_encoder_missing");
    }

    const Requested requested = parse_requested(std::getenv(kBackendEnv));
    if (requested == Requested::kCpu) {
        ZL_LOGI(0, kModule, "jpeg_backend_selected: software (requested)");
        return;
    }

    auto hardware = MppJpegEncoder::create();
    if (hardware == nullptr) {
        ZL_LOGI(0, kModule, "jpeg_backend_selected: software (hardware unavailable)");
        return;
    }

    // ⚠️ 自检不可跳过：它拦的是「编码成功但恒为纯灰图且不报错」这类静默错误。
    //    开销是一次 32x32 编码（亚毫秒），与它避免的错误量级完全不成比例。
    if (!hardware->self_test()) {
        ZL_LOGW(0, kModule, "jpeg_hardware_self_test_failed: falling_back_to_software");
        return;
    }

    hw_ = std::move(hardware);
    kind_ = JpegBackendKind::kMpp;
    ZL_LOGI(0, kModule, "jpeg_backend_selected: mpp (vepu)");
}

bool JpegBackend::encode_dma(const HardwareFrame &frame, const CpuFrameView &view, int w, int h,
                             int quality, std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    if (hw_ == nullptr || dma_state_.load(std::memory_order_relaxed) == DmaState::kDisabled ||
        !prefer_hardware(w, h)) {
        return false;
    }

    if (hw_->encode_dma_frame(frame, quality, out_jpeg)) {
        dma_failures_.store(0, std::memory_order_relaxed);

        // 首次自证：对同一帧的两条路径做逐字节比对（仅一次，代价约一次编码）
        if (dma_state_.load(std::memory_order_relaxed) == DmaState::kUnverified) {
            std::vector<uint8_t> reference;
            if (!hw_->encode_region(view, 0, 0, w, h, quality, reference)) {
                // 参考路径失败 → 本次不下结论，让调用方走常规路径，下次再试
                out_jpeg.clear();
                return false;
            }
            if (reference == out_jpeg) {
                if (dma_state_.exchange(DmaState::kValidated, std::memory_order_relaxed) ==
                    DmaState::kUnverified) {
                    ZL_LOGI(0, kModule, "jpeg_dmabuf_passthrough_validated: %dx%d", w, h);
                }
            } else {
                dma_state_.store(DmaState::kDisabled, std::memory_order_relaxed);
                ZL_LOGE(0, kModule,
                        "jpeg_dmabuf_passthrough_mismatch: disabled (passthrough=%zu B "
                        "reference=%zu B)",
                        out_jpeg.size(), reference.size());
                out_jpeg.clear();
                return false;
            }
        }
        return true;
    }

    // 连续失败 → 永久关闭：避免「每帧先付 import 开销再回退」的隐性退化
    if (dma_failures_.fetch_add(1, std::memory_order_relaxed) + 1 >= kMaxDmaFailures) {
        if (dma_state_.exchange(DmaState::kDisabled, std::memory_order_relaxed) !=
            DmaState::kDisabled) {
            ZL_LOGW(0, kModule, "jpeg_dmabuf_passthrough_disabled: consecutive_failures=%d",
                    kMaxDmaFailures);
        }
    }
    return false;
}

bool JpegBackend::encode(const CpuFrameView &src, int x, int y, int w, int h, int quality,
                         std::vector<uint8_t> &out_jpeg) {
    if (prefer_hardware(w, h) && hw_->encode_region(src, x, y, w, h, quality, out_jpeg)) {
        return true;
    }
    if (!software_available_) {
        out_jpeg.clear();
        return false;
    }
    YuvFrame frame{};
    if (!extract_yuv_region(src, x, y, w, h, frame)) {
        out_jpeg.clear();
        return false;
    }
    return encode_jpeg(frame, quality, out_jpeg);
}

} // namespace zhulong
