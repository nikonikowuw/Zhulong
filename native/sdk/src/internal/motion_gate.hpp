/**
 * @file motion_gate.hpp
 * @brief Runner 骨架的帧运动门禁：报文解码与前向短路判定的纯函数
 *
 * 为什么单独成文件：这两条判据是「省算力」与「不漏检」之间的安全边界，
 * 必须能被单测直接钉住，而不是靠板端跑一遍看结果像不像。
 *
 * 归属：编译进 `libzhulong_algo_sdk.a` 的骨架（`algo_main.cpp`），随 SDK 前缀分发。
 */

#pragma once

#include "algorithm/ipc/ipc_protocol.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstring>

namespace zhulong::sdk::gate {

/**
 * @brief 从 MSG_FRAME_DISPATCH 负载解码帧分发报文（兼容遗留 96 字节线格式）
 *
 * - `size >= sizeof(FrameDispatchMsg)`：按当前格式整体解析；
 * - `size ∈ [LEGACY_FRAME_DISPATCH_SIZE, sizeof(FrameDispatchMsg))`：按遗留格式解析，
 *   运动字段保持默认值（`has_motion = 1` 放行）。新 Runner 连旧宿主时必须退化为
 *   「全帧推理」，绝不能因为读不到运动字段就既不推理也不 ACK；
 * - 更短：连遗留前缀都不完整，返回 false，调用方按畸形报文丢弃。
 *
 * @param payload 负载起始地址
 * @param size 负载字节数
 * @param[out] out 解码结果（失败时被重置为默认值）
 * @return true 解码成功；false 载荷过短
 */
inline bool decode_frame_dispatch(const void* payload, size_t size,
                                  ipc::FrameDispatchMsg& out) noexcept {
    out = ipc::FrameDispatchMsg{};
    if (payload == nullptr || size < ipc::LEGACY_FRAME_DISPATCH_SIZE) {
        return false;
    }

    std::memcpy(&out, payload, std::min(size, sizeof(out)));

    if (size < sizeof(ipc::FrameDispatchMsg)) {
        // 遗留报文：没有运动字段，一律放行。out 已在函数入口整体重置，
        // 这里只需要把可能被半截拷贝写脏的运动字段拉回默认值。
        out.has_motion = 1;
        out.motion_box_count = 0;
        for (auto& box : out.motion_boxes) {
            box = ipc::IpcMotionBox{};
        }
    }
    return true;
}

/**
 * @brief 是否短路 `api.process()` 与模型前向
 *
 * @param has_motion 帧分发报文中的运动标记（0 表示静止，1 表示有运动）
 * @param motion_gate_enabled 算法/进程是否启用了运动门禁（默认启用；若被 --disable-motion-gate 禁用则恒不短路）
 *
 * ⚠️ 只有在门禁启用且显式 `has_motion == 0` 时才跳过。其他任何取值（含未来新增值、半截报文带来的
 *    默认值或禁用门禁模式）一律放行：漏检的代价远高于一次多余的推理。
 */
inline bool should_skip_inference(uint32_t has_motion, bool motion_gate_enabled = true) noexcept {
    if (!motion_gate_enabled) {
        return false;
    }
    return has_motion == 0;
}

} // namespace zhulong::sdk::gate
