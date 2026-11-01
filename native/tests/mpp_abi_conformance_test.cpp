/**
 * @file mpp_abi_conformance_test.cpp
 * @brief Rockchip MPP 手工镜像与厂商头文件的编译期一致性测试
 *
 * 背景：`native/src/common/mpp/mpp_api_types.hpp` 是**手写镜像**（为了在 x86/macOS 上
 * 不依赖厂商 SDK 也能编译），而宿主的构建里从不引用厂商头文件。镜像一旦与 BSP 的
 * `mpp_buffer.h` / `mpi.h` / `rk_type.h` 漂移，表现不是编译失败，而是板端「偶发斜切图」
 * 「DMA-BUF fd 传错位」「命令字打错」这类静默错误。
 *
 * 因此把这个风险变成编译期断言：本文件**刻意**同时引入两份声明（镜像 + 仓库内 vendored
 * 厂商头 `native/vendor/mpp/include/rockchip/`），逐项比对 struct 布局、字段偏移、
 * 枚举值与 MppCmd 命令字。厂商升级时这里先炸，而不是板端。
 *
 * ⚠️ 本测试不链接 librockchip_mpp：只做类型与常量比对，因此主机与交叉构建都能跑。
 */

#include "common/mpp/mpp_api_types.hpp"

extern "C" {
#include "mpp_buffer.h"
#include "mpp_err.h"
#include "mpp_frame.h"
#include "mpp_packet.h"
#include "rk_mpi.h"
#include "rk_mpi_cmd.h"
#include "rk_venc_cfg.h"
#include "rk_type.h"
}

#include <cstddef>
#include <cstdio>

namespace mirror = zhulong::mpp;

// ============================================================================
// 1. MppBufferInfo：字段顺序错一位，`mpp_buffer_import` 就会从 hnd 槽位读 fd
// ============================================================================
static_assert(sizeof(mirror::BufferInfo) == sizeof(MppBufferInfo),
              "BufferInfo mirror size drifted from vendor mpp_buffer.h");
static_assert(offsetof(mirror::BufferInfo, type) == offsetof(MppBufferInfo, type));
static_assert(offsetof(mirror::BufferInfo, size) == offsetof(MppBufferInfo, size));
static_assert(offsetof(mirror::BufferInfo, ptr) == offsetof(MppBufferInfo, ptr));
static_assert(offsetof(mirror::BufferInfo, hnd) == offsetof(MppBufferInfo, hnd));
static_assert(offsetof(mirror::BufferInfo, fd) == offsetof(MppBufferInfo, fd));
static_assert(offsetof(mirror::BufferInfo, index) == offsetof(MppBufferInfo, index));

// ============================================================================
// 2. MppApi 函数表：镜像只用到 decode*/encode*/reset/control，但偏移必须整体一致
//    （少一个字段就会让 control 指向 encode_get_packet）
// ============================================================================
static_assert(sizeof(mirror::MppApi) == sizeof(::MppApi),
              "MppApi mirror size drifted from vendor rk_mpi.h");
static_assert(offsetof(mirror::MppApi, size) == offsetof(::MppApi, size));
static_assert(offsetof(mirror::MppApi, version) == offsetof(::MppApi, version));
static_assert(offsetof(mirror::MppApi, decode) == offsetof(::MppApi, decode));
static_assert(offsetof(mirror::MppApi, decode_put_packet) == offsetof(::MppApi, decode_put_packet));
static_assert(offsetof(mirror::MppApi, decode_get_frame) == offsetof(::MppApi, decode_get_frame));
static_assert(offsetof(mirror::MppApi, encode) == offsetof(::MppApi, encode));
static_assert(offsetof(mirror::MppApi, encode_put_frame) == offsetof(::MppApi, encode_put_frame));
static_assert(offsetof(mirror::MppApi, encode_get_packet) ==
              offsetof(::MppApi, encode_get_packet));
static_assert(offsetof(mirror::MppApi, isp) == offsetof(::MppApi, isp));
static_assert(offsetof(mirror::MppApi, isp_put_frame) == offsetof(::MppApi, isp_put_frame));
static_assert(offsetof(mirror::MppApi, isp_get_frame) == offsetof(::MppApi, isp_get_frame));
static_assert(offsetof(mirror::MppApi, poll) == offsetof(::MppApi, poll));
static_assert(offsetof(mirror::MppApi, dequeue) == offsetof(::MppApi, dequeue));
static_assert(offsetof(mirror::MppApi, enqueue) == offsetof(::MppApi, enqueue));
static_assert(offsetof(mirror::MppApi, reset) == offsetof(::MppApi, reset));
static_assert(offsetof(mirror::MppApi, control) == offsetof(::MppApi, control));

// ============================================================================
// 3. 编码类型 / 上下文类型：错一个值，mpp_init 直接 MPP_NOK 或初始化成错的 ctx
// ============================================================================
static_assert(mirror::MPP_CTX_DEC == MPP_CTX_DEC);
static_assert(mirror::MPP_CTX_ENC == MPP_CTX_ENC);
static_assert(mirror::MPP_CTX_ISP == MPP_CTX_ISP);
static_assert(mirror::MPP_VIDEO_CodingAVC == MPP_VIDEO_CodingAVC);
static_assert(mirror::MPP_VIDEO_CodingHEVC == MPP_VIDEO_CodingHEVC);
static_assert(mirror::MPP_VIDEO_CodingMJPEG == MPP_VIDEO_CodingMJPEG);

// ============================================================================
// 4. 像素格式与缓冲类型：错一个值，VEPU 会把 NV12 当 10bit 读，或从 DRM 堆取不到内存
// ============================================================================
static_assert(mirror::MPP_FMT_YUV420SP == MPP_FMT_YUV420SP);
static_assert(mirror::MPP_FMT_YUV420SP_10BIT == MPP_FMT_YUV420SP_10BIT);
static_assert(mirror::MPP_BUFFER_TYPE_NORMAL == MPP_BUFFER_TYPE_NORMAL);
static_assert(mirror::MPP_BUFFER_TYPE_ION == MPP_BUFFER_TYPE_ION);
static_assert(mirror::MPP_BUFFER_TYPE_EXT_DMA == MPP_BUFFER_TYPE_EXT_DMA);
static_assert(mirror::MPP_BUFFER_TYPE_DRM == MPP_BUFFER_TYPE_DRM);
static_assert(mirror::MPP_BUFFER_TYPE_DMA_HEAP == MPP_BUFFER_TYPE_DMA_HEAP);
static_assert(mirror::MPP_BUFFER_INTERNAL == MPP_BUFFER_INTERNAL);
static_assert(mirror::MPP_BUFFER_EXTERNAL == MPP_BUFFER_EXTERNAL);

// ============================================================================
// 5. 命令字：错一个值，control() 会静默打到别的命令上（MPP 对未知 cmd 常返回 MPP_NOK，
//    但相邻 cmd 的错误命中是可能的，且必然表现为「配置没生效」）
// ============================================================================
static_assert(mirror::MPP_SET_INPUT_TIMEOUT == MPP_SET_INPUT_TIMEOUT);
static_assert(mirror::MPP_SET_OUTPUT_TIMEOUT == MPP_SET_OUTPUT_TIMEOUT);
static_assert(mirror::MPP_DEC_SET_FRAME_INFO == MPP_DEC_SET_FRAME_INFO);
static_assert(mirror::MPP_DEC_SET_EXT_BUF_GROUP == MPP_DEC_SET_EXT_BUF_GROUP);
static_assert(mirror::MPP_DEC_SET_INFO_CHANGE_READY == MPP_DEC_SET_INFO_CHANGE_READY);
static_assert(mirror::MPP_DEC_SET_PRESENT_TIME_ORDER == MPP_DEC_SET_PRESENT_TIME_ORDER);
static_assert(mirror::MPP_DEC_SET_PARSER_SPLIT_MODE == MPP_DEC_SET_PARSER_SPLIT_MODE);
static_assert(mirror::MPP_DEC_SET_PARSER_FAST_MODE == MPP_DEC_SET_PARSER_FAST_MODE);
static_assert(mirror::MPP_DEC_SET_OUTPUT_FORMAT == MPP_DEC_SET_OUTPUT_FORMAT);
static_assert(mirror::MPP_DEC_SET_IMMEDIATE_OUT == MPP_DEC_SET_IMMEDIATE_OUT);
static_assert(mirror::MPP_DEC_SET_ENABLE_FAST_PLAY == MPP_DEC_SET_ENABLE_FAST_PLAY);
static_assert(mirror::MPP_ENC_SET_CFG == MPP_ENC_SET_CFG);

// ============================================================================
// 6. 错误码：背压/超时的归一化逻辑直接依赖这些数值（MPP_ERR_BUFFER_FULL 必须仍为 -1012）
// ============================================================================
static_assert(mirror::MPP_OK == MPP_OK);
static_assert(mirror::MPP_NOK == MPP_NOK);
static_assert(mirror::MPP_ERR_TIMEOUT == MPP_ERR_TIMEOUT);
static_assert(mirror::MPP_ERR_BASE == MPP_ERR_BASE);
static_assert(mirror::MPP_ERR_BUFFER_FULL == MPP_ERR_BUFFER_FULL);

/**
 * @brief 断言只在编译期成立；运行期只打印一次，便于 ctest 日志确认「确实跑到了」
 */
int main() {
    std::printf("mpp_abi_conformance: mirror matches vendor headers "
                "(MppBufferInfo=%zu B, MppApi=%zu B)\n",
                sizeof(MppBufferInfo), sizeof(::MppApi));
    std::printf("  coding: avc=%d hevc=%d mjpeg=%d\n", MPP_VIDEO_CodingAVC,
                MPP_VIDEO_CodingHEVC, MPP_VIDEO_CodingMJPEG);
    std::printf("  cmds: set_ext_buf_group=0x%08x enc_set_cfg=0x%08x\n",
                static_cast<unsigned>(MPP_DEC_SET_EXT_BUF_GROUP),
                static_cast<unsigned>(MPP_ENC_SET_CFG));
    return 0;
}
