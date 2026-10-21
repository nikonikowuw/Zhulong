/**
 * @file mpp_api_types.hpp
 * @brief Rockchip MPP (Media Process Platform) 纯头文件类型与 API 函数表契约
 *
 * 为 MPP 硬件解码节点提供跨平台可编译的结构体定义、枚举值与动态装载函数表。
 * 不依赖宿主系统的编译期 SDK 依赖，支持在 x86_64/macOS 下安全静态检查。
 */

#pragma once

#include <cstddef>
#include <cstdint>

namespace zhulong::mpp {

using MppCtx = void*;
using MppParam = void*;
using MppBuffer = void*;
using MppBufferGroup = void*;
using MppPacket = void*;
using MppFrame = void*;
using MppEncCfg = void*;

using MppCtxType = int32_t;
constexpr MppCtxType MPP_CTX_DEC = 0;
constexpr MppCtxType MPP_CTX_ENC = 1;
constexpr MppCtxType MPP_CTX_ISP = 2;

using MppCodingType = int32_t;
constexpr MppCodingType MPP_VIDEO_CodingAVC   = 7;        /**< H.264/AVC */
constexpr MppCodingType MPP_VIDEO_CodingMJPEG = 8;        /**< Motion JPEG（对齐 rk_type.h 的 MppCodingType 序号） */
constexpr MppCodingType MPP_VIDEO_CodingHEVC  = 16777220; /**< H.265/HEVC (0x01000004) */

using MppFrameFormat = uint32_t;
constexpr MppFrameFormat MPP_FMT_YUV420SP       = 0; /**< NV12: Y plane + UV interleaved */
constexpr MppFrameFormat MPP_FMT_YUV420SP_10BIT = 1; /**< NV12 10bit */

using MppBufferType = int32_t;
constexpr MppBufferType MPP_BUFFER_TYPE_NORMAL   = 0;
constexpr MppBufferType MPP_BUFFER_TYPE_ION      = 1;
constexpr MppBufferType MPP_BUFFER_TYPE_EXT_DMA  = 2;
constexpr MppBufferType MPP_BUFFER_TYPE_DRM      = 3;
constexpr MppBufferType MPP_BUFFER_TYPE_DMA_HEAP = 4;

using MppBufferMode = int32_t;
constexpr MppBufferMode MPP_BUFFER_INTERNAL = 0;
constexpr MppBufferMode MPP_BUFFER_EXTERNAL = 1;

// MPP 命令字定义
constexpr int32_t CMD_MODULE_MPP   = 0x00200000;
constexpr int32_t MPP_CMD_BASE     = CMD_MODULE_MPP;
constexpr int32_t MPP_SET_INPUT_TIMEOUT  = MPP_CMD_BASE + 6; /**< 0x00200006: parameter int64_t ms */
constexpr int32_t MPP_SET_OUTPUT_TIMEOUT = MPP_CMD_BASE + 7; /**< 0x00200007: parameter int64_t ms */

constexpr int32_t CMD_MODULE_CODEC = 0x00300000;
constexpr int32_t CMD_CTX_ID_DEC   = 0x00010000;
constexpr int32_t MPP_DEC_CMD_BASE = CMD_MODULE_CODEC | CMD_CTX_ID_DEC; /**< 0x00310000 */

constexpr int32_t MPP_DEC_SET_FRAME_INFO         = MPP_DEC_CMD_BASE + 1; /**< 0x00310001 */
constexpr int32_t MPP_DEC_SET_EXT_BUF_GROUP      = MPP_DEC_CMD_BASE + 2; /**< 0x00310002 */
constexpr int32_t MPP_DEC_SET_INFO_CHANGE_READY  = MPP_DEC_CMD_BASE + 3; /**< 0x00310003 */
constexpr int32_t MPP_DEC_SET_PRESENT_TIME_ORDER = MPP_DEC_CMD_BASE + 4; /**< 0x00310004 */
constexpr int32_t MPP_DEC_SET_PARSER_SPLIT_MODE  = MPP_DEC_CMD_BASE + 5; /**< 0x00310005 */
constexpr int32_t MPP_DEC_SET_PARSER_FAST_MODE   = MPP_DEC_CMD_BASE + 6; /**< 0x00310006 */
constexpr int32_t MPP_DEC_SET_OUTPUT_FORMAT      = MPP_DEC_CMD_BASE + 10;/**< 0x0031000a */
constexpr int32_t MPP_DEC_SET_IMMEDIATE_OUT      = MPP_DEC_CMD_BASE + 12;/**< 0x0031000c */
constexpr int32_t MPP_DEC_SET_ENABLE_FAST_PLAY   = MPP_DEC_CMD_BASE + 14;/**< 0x0031000e */

constexpr int32_t CMD_CTX_ID_ENC   = 0x00020000;
constexpr int32_t MPP_ENC_CMD_BASE = CMD_MODULE_CODEC | CMD_CTX_ID_ENC; /**< 0x00320000 */

/** @brief 0x00320001：下完整 MppEncCfg（含 prep / codec / rc / jpeg 子项） */
constexpr int32_t MPP_ENC_SET_CFG = MPP_ENC_CMD_BASE + 1;

// MPP 错误码
constexpr int32_t MPP_OK              = 0;
constexpr int32_t MPP_NOK             = -1;
constexpr int32_t MPP_ERR_TIMEOUT     = -8;
constexpr int32_t MPP_ERR_BASE        = -1000;
constexpr int32_t MPP_ERR_BUFFER_FULL = MPP_ERR_BASE - 12; /**< -1012 */

/**
 * @brief Rockchip MPP Mpi 函数表
 */
struct MppApi {
    uint32_t size;
    uint32_t version;

    int (*decode)(MppCtx ctx, MppPacket packet, MppFrame *frame);
    int (*decode_put_packet)(MppCtx ctx, MppPacket packet);
    int (*decode_get_frame)(MppCtx ctx, MppFrame *frame);

    int (*encode)(MppCtx ctx, MppFrame frame, MppPacket *packet);
    int (*encode_put_frame)(MppCtx ctx, MppFrame frame);
    int (*encode_get_packet)(MppCtx ctx, MppPacket *packet);

    int (*isp)(MppCtx ctx, MppFrame dst, MppFrame src);
    int (*isp_put_frame)(MppCtx ctx, MppFrame frame);
    int (*isp_get_frame)(MppCtx ctx, MppFrame *frame);

    int (*poll)(MppCtx ctx, int port_type, int timeout);
    int (*dequeue)(MppCtx ctx, int port_type, void *task);
    int (*enqueue)(MppCtx ctx, int port_type, void *task);

    int (*reset)(MppCtx ctx);
    int (*control)(MppCtx ctx, int32_t cmd, MppParam param);
    uint32_t reserv[16];
};

} // namespace zhulong::mpp
