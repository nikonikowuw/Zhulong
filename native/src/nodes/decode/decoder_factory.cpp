/**
 * @file decoder_factory.cpp
 * @brief 解码节点工厂：按后端策略与宿主能力选择软解 / 硬解实现
 *
 * 核心实现规范：
 * 1. 能力探测与回退：`AUTO` 优先 MPP 硬解，探测或初始化失败时透明回退软解；显式 `MPP_HW` 失败则抛错（fail closed）；
 * 2. 日志上下文：所有初始化失败都带 `stream_id` 与稳定 token + 数值上下文（不含自由文本），
 *    板端无需打开调试器即可定位后端问题；
 * 3. 依赖方向：抽象接口的工厂独立成编译单元，不寄生在任何具体后端实现里。
 */

#include "nodes/decode/decoder.hpp"
#include "nodes/decode/ffmpeg_decoder.hpp"
#include "nodes/decode/mpp_decoder.hpp"
#include "common/log.hpp"

#include <memory>
#include <stdexcept>

namespace zhulong {

IDecodeNode::Ptr IDecodeNode::create(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                     int32_t time_base_num, int32_t time_base_den,
                                     DecoderBackend backend, uint64_t stream_id) {
    if (backend == DecoderBackend::AUTO || backend == DecoderBackend::MPP_HW) {
        if (MppDecodeNode::is_supported()) {
            try {
                return std::make_unique<MppDecodeNode>(codec, extradata, extradata_size, time_base_num,
                                                       time_base_den, stream_id);
            } catch (...) {
                if (backend == DecoderBackend::MPP_HW) {
                    ZL_LOGE(stream_id, "nodes.decode", "mpp_decoder_init_failed codec=%d", codec);
                    throw;
                }
                ZL_LOGW(stream_id, "nodes.decode", "mpp_decoder_init_failed_fallback codec=%d", codec);
            }
        } else if (backend == DecoderBackend::MPP_HW) {
            ZL_LOGE(stream_id, "nodes.decode", "mpp_decoder_unavailable codec=%d", codec);
            throw std::runtime_error("MPP hardware decoder requested but not supported on this host");
        } else {
            ZL_LOGI(stream_id, "nodes.decode", "mpp_unavailable_fallback codec=%d", codec);
        }
    }

    try {
        return std::make_unique<FFmpegDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den);
    } catch (...) {
        // ⚠️ 日志纪律：只输出稳定 token + 数值上下文，不回写可能导致泄漏的异常文本
        ZL_LOGE(stream_id, "nodes.decode", "ffmpeg_decoder_init_failed codec=%d", codec);
        throw;
    }
}

} // namespace zhulong
