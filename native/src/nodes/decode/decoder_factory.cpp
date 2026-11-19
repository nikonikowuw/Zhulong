/**
 * @file decoder_factory.cpp
 * @brief 解码节点工厂：按后端策略与宿主能力选择软解 / 硬解实现
 *
 * 核心实现规范：
 * 1. 能力探测与回退：`automatic` 依次尝试 MPP、昇腾 VDEC，探测或初始化失败时透明回退软解；
 *    显式硬件后端（`mpp_hw` / `ascend_vdec` / `hardware_auto`）失败则抛错（fail closed），
 *    绝不静默降级成「以为在硬解、其实在软解」；
 * 2. 日志上下文：所有初始化失败都带 `stream_id` 与稳定 token + 数值上下文（不含自由文本），
 *    板端无需打开调试器即可定位后端问题；
 * 3. 依赖方向：抽象接口的工厂独立成编译单元，不寄生在任何具体后端实现里。
 */

#include "nodes/decode/decoder.hpp"
#include "nodes/decode/ascend_decoder.hpp"
#include "nodes/decode/ascend_vdec.hpp"
#include "nodes/decode/ffmpeg_decoder.hpp"
#include "nodes/decode/mpp_decoder.hpp"
#include "common/log.hpp"

#include <memory>
#include <stdexcept>

namespace zhulong {

namespace {

/** @brief 昇腾探测 + 创建；返回 nullptr 表示自动模式应继续回退 */
IDecodeNode::Ptr try_create_ascend(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                   int32_t time_base_num, int32_t time_base_den, uint64_t stream_id,
                                   int32_t width, int32_t height, bool explicit_backend) {
    if (!AscendDecodeNode::is_supported()) {
        if (explicit_backend) {
            ZL_LOGE(stream_id, "nodes.decode", "ascend_vdec_unavailable codec=%d compiled=%d", codec,
                    ascend::ascend_vdec_compiled() ? 1 : 0);
            throw std::runtime_error("Ascend VDEC requested but not available on this host");
        }
        ZL_LOGI(stream_id, "nodes.decode", "ascend_vdec_unavailable_fallback codec=%d compiled=%d", codec,
                ascend::ascend_vdec_compiled() ? 1 : 0);
        return nullptr;
    }

    try {
        return std::make_unique<AscendDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den,
                                                  stream_id, width, height);
    } catch (...) {
        if (explicit_backend) {
            ZL_LOGE(stream_id, "nodes.decode", "ascend_vdec_init_failed codec=%d width=%d height=%d", codec, width,
                    height);
            throw;
        }
        ZL_LOGW(stream_id, "nodes.decode", "ascend_vdec_init_failed_fallback codec=%d width=%d height=%d", codec,
                width, height);
        return nullptr;
    }
}

} // namespace

IDecodeNode::Ptr IDecodeNode::create(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                     int32_t time_base_num, int32_t time_base_den,
                                     DecoderBackend backend, uint64_t stream_id, int32_t width, int32_t height) {
    const bool hardware_only = backend == DecoderBackend::hardware_auto;
    const bool allow_mpp = backend == DecoderBackend::automatic || hardware_only || backend == DecoderBackend::mpp_hw;
    const bool allow_ascend =
        backend == DecoderBackend::automatic || hardware_only || backend == DecoderBackend::ascend_vdec;

    if (allow_mpp) {
        if (MppDecodeNode::is_supported()) {
            try {
                return std::make_unique<MppDecodeNode>(codec, extradata, extradata_size, time_base_num,
                                                       time_base_den, stream_id);
            } catch (...) {
                if (backend == DecoderBackend::mpp_hw) {
                    ZL_LOGE(stream_id, "nodes.decode", "mpp_decoder_init_failed codec=%d", codec);
                    throw;
                }
                ZL_LOGW(stream_id, "nodes.decode", "mpp_decoder_init_failed_fallback codec=%d", codec);
            }
        } else if (backend == DecoderBackend::mpp_hw) {
            ZL_LOGE(stream_id, "nodes.decode", "mpp_decoder_unavailable codec=%d", codec);
            throw std::runtime_error("MPP hardware decoder requested but not supported on this host");
        } else {
            ZL_LOGI(stream_id, "nodes.decode", "mpp_unavailable_fallback codec=%d", codec);
        }
    }

    if (allow_ascend) {
        const bool explicit_backend = backend == DecoderBackend::ascend_vdec;
        if (IDecodeNode::Ptr node = try_create_ascend(codec, extradata, extradata_size, time_base_num, time_base_den,
                                                      stream_id, width, height, explicit_backend)) {
            return node;
        }
    }

    if (hardware_only) {
        // 调用方明确禁止软解：这里必须抛错，让上层进入 fail-closed 分支
        ZL_LOGE(stream_id, "nodes.decode", "hardware_decoder_unavailable codec=%d", codec);
        throw std::runtime_error("no hardware decoder available and software fallback is disabled");
    }

    try {
        return std::make_unique<FFmpegDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den,
                                                  stream_id);
    } catch (...) {
        // ⚠️ 日志纪律：只输出稳定 token + 数值上下文，不回写可能导致泄漏的异常文本
        ZL_LOGE(stream_id, "nodes.decode", "ffmpeg_decoder_init_failed codec=%d", codec);
        throw;
    }
}

} // namespace zhulong
