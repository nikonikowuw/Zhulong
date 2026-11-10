/**
 * @file jpeg_encoder_backend.cpp
 * @brief JPEG 编码后端选择实现
 */

#include "nodes/snapshot/jpeg_encoder_backend.hpp"

#include "common/log.hpp"
#include "nodes/snapshot/mpp_jpeg_encoder.hpp"

#include <cstdlib>
#include <cstring>
#include <new>

namespace zhulong {
namespace {

constexpr const char *log_module = "nodes.snapshot.jpeg";

/** @brief 后端选择环境变量名 */
constexpr const char *backend_env = "ZHULONG_JPEG_BACKEND";

/** @brief 环境变量取值 */
enum class Requested {
    auto_select,
    cpu,
    mpp,
};

Requested parse_requested(const char *raw) {
    if (raw == nullptr || raw[0] == '\0' || std::strcmp(raw, "auto") == 0) {
        return Requested::auto_select;
    }
    if (std::strcmp(raw, "cpu") == 0) {
        return Requested::cpu;
    }
    if (std::strcmp(raw, "mpp") == 0) {
        return Requested::mpp;
    }
    // fail closed：未知取值按 cpu 处理，避免拼写错误静默开启硬件路径。
    // ⚠️ 日志纪律：不回写环境变量原文（含换行即可伪造日志行），只给稳定 token + 长度。
    ZL_LOGE(0, log_module, "invalid_jpeg_backend_value len=%zu",
            raw == nullptr ? 0U : std::strlen(raw));
    return Requested::cpu;
}

} // namespace

JpegBackend &JpegBackend::instance() {
    // 刻意不在进程退出时析构（同 MppLoader::instance）：
    // librockchip_mpp 内部后台线程/全局状态在 __cxa_finalize 阶段顺序未定义，
    // 静态析构里调 mpp_destroy / dlclose 易触发退出期崩溃，OS 回收进程资源更安全。
    alignas(JpegBackend) static unsigned char storage[sizeof(JpegBackend)];
    static JpegBackend *backend = new (storage) JpegBackend();
    return *backend;
}

const char *JpegBackend::kind_name(JpegBackendKind kind) noexcept {
    return kind == JpegBackendKind::hardware ? "mpp" : "software";
}

JpegBackend::JpegBackend() {
    software_available_ = jpeg_encoder_available();
    if (!software_available_) {
        // 软件编码器缺失不是致命错误：只要硬件可用，抓拍依然成立
        ZL_LOGW(0, log_module, "software_jpeg_encoder_missing");
    }

    const Requested requested = parse_requested(std::getenv(backend_env));
    if (requested == Requested::cpu) {
        ZL_LOGI(0, log_module, "jpeg_backend_selected: software (requested)");
        return;
    }

    auto hardware = MppJpegEncoder::create();
    if (hardware == nullptr) {
        ZL_LOGI(0, log_module, "jpeg_backend_selected: software (hardware unavailable)");
        return;
    }

    // ⚠️ 自检不可跳过：它拦的是「编码成功但恒为纯灰图且不报错」这类静默错误。
    //    开销是一次 32x32 编码（亚毫秒），与它避免的错误量级完全不成比例。
    if (!hardware->self_test()) {
        ZL_LOGW(0, log_module, "jpeg_hardware_self_test_failed: falling_back_to_software");
        return;
    }

    hw_ = std::move(hardware);
    kind_ = JpegBackendKind::hardware;
    ZL_LOGI(0, log_module, "jpeg_backend_selected: mpp (vepu)");
}

bool JpegBackend::encode_dma(const HardwareFrame &frame, const CpuFrameView &view, int w, int h,
                             int quality, std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    const int full_w = static_cast<int>(frame.width & ~1U);
    const int full_h = static_cast<int>(frame.height & ~1U);
    if (hw_ == nullptr || dma_state_.load(std::memory_order_relaxed) == DmaState::disabled ||
        w != full_w || h != full_h || !prefer_hardware(w, h)) {
        return false;
    }

    if (hw_->encode_dma_frame(frame, quality, out_jpeg)) {
        dma_failures_.store(0, std::memory_order_relaxed);

        // 首次自证：对同一帧的两条路径做逐字节比对（仅一次，代价约一次编码）
        if (dma_state_.load(std::memory_order_relaxed) == DmaState::unverified) {
            std::vector<uint8_t> reference;
            if (!hw_->encode_region(view, 0, 0, w, h, quality, reference)) {
                // 参考路径失败 → 本次不下结论，让调用方走常规路径，下次再试
                out_jpeg.clear();
                return false;
            }
            if (reference == out_jpeg) {
                DmaState expected = DmaState::unverified;
                if (dma_state_.compare_exchange_strong(expected, DmaState::validated,
                                                       std::memory_order_relaxed)) {
                    ZL_LOGI(0, log_module, "jpeg_dmabuf_passthrough_validated: %dx%d", w, h);
                } else if (expected == DmaState::disabled) {
                    out_jpeg.clear();
                    return false;
                }
            } else {
                dma_state_.store(DmaState::disabled, std::memory_order_relaxed);
                ZL_LOGE(0, log_module,
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
    if (dma_failures_.fetch_add(1, std::memory_order_relaxed) + 1 >= max_dma_failures) {
        if (dma_state_.exchange(DmaState::disabled, std::memory_order_relaxed) !=
            DmaState::disabled) {
            ZL_LOGW(0, log_module, "jpeg_dmabuf_passthrough_disabled: consecutive_failures=%d",
                    max_dma_failures);
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
