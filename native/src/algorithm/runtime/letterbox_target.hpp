/**
 * @file letterbox_target.hpp
 * @brief Letterbox 目标 Tensor 的「复用还是重建」决策与补边填充值（后端无关）
 *
 * 核心设计原则：
 * 1. **决策只有一份**：CPU 与 RGA 两个视觉后端必须对「什么样的 dst 可以原地复用」
 *    给出完全一致的答案，否则同一份调用在两个后端下会得到不同的缓冲区所有权语义
 *    （最坏情况是 RGA 失败后 CPU 又按另一套规则改写了调用方的 dst）。
 * 2. **失败语义统一**：Tensor 构造以异常报告，本函数就地转成状态码，避免冒泡到
 *    C ABI 的 catch (...) 里变成无法区分的通用错误。
 * 3. 只做「准备缓冲区」，不写像素：补边填充由调用方在确认硬件路径可行后再执行
 *    （O(N) 的 memset 不该为一次注定回退的尝试白付）。
 */

#pragma once

#include <zhulong/plugin.h>
#include <zhulong/runtime/tensor.hpp>

#include <cstddef>
#include <cstdint>
#include <new>
#include <stdexcept>
#include <vector>

namespace zhulong {

/**
 * @brief Letterbox 补边灰度值（与训练侧预处理对齐，非 0）
 *
 * ⚠️ 曾是 CPU 后端的私有常量、RGA 后端的裸字面量 114：两份「同一灰度」的实现
 *    靠人肉对齐，任一侧改动都会让两个后端的补边值静默分叉。
 */
inline constexpr uint8_t letterbox_pad_value = 114;

enum class LetterboxTargetAction { reuse, rebind_dma, allocate_cpu };

// RGA 用同一份决策暂存更新，避免预校验失败时改变调用方的 Tensor 元数据/所有权。
inline LetterboxTargetAction letterbox_target_action(const Tensor& dst,
                                                     const std::vector<int64_t>& target_shape,
                                                     std::size_t required_bytes) noexcept {
    const bool dma_reusable = dst.dma_buf_fd() >= 0 && dst.raw_data() != nullptr &&
                              dst.byte_size() >= required_bytes;
    const bool shape_matches = dst.dtype() == DataType::UINT8 &&
        (dst.shape() == target_shape ||
         (dst.shape().size() == 4 && dst.shape()[0] == 1 &&
          target_shape.size() == 3 &&
          dst.shape()[1] == target_shape[0] &&
          dst.shape()[2] == target_shape[1] &&
          dst.shape()[3] == target_shape[2]));
    if (dma_reusable) {
        return shape_matches ? LetterboxTargetAction::reuse : LetterboxTargetAction::rebind_dma;
    }
    if (dst.dma_buf_fd() < 0 && dst.raw_data() != nullptr && dst.byte_size() >= required_bytes && shape_matches) {
        return LetterboxTargetAction::reuse;
    }
    return LetterboxTargetAction::allocate_cpu;
}

/**
 * @brief 按目标形状准备 dst 缓冲（能原地复用就不重新分配）
 *
 * 复用规则：
 * - `dst` 绑定了 DMA-BUF 且已映射（`raw_data() != nullptr`）且容量够 → 保留 fd，
 *   仅在 dtype/形状不符时就地重绑（不重新分配）；
 * - `dst` 是普通内存且容量够、dtype 与形状都匹配 → 原地复用（热路径零分配）；
 * - 其余情况（含「绑定了 DMA-BUF 但没有映射」）→ 分配新的 UINT8 普通内存张量。
 *
 * @param[in,out] dst 目标张量（按上述规则就地复用或重建）
 * @param target_shape 目标形状（HWC、3 通道）
 * @param required_bytes 目标字节数（调用方已完成溢出校验）
 * @return zhulong_status_t OK / OUT_OF_MEMORY
 */
inline zhulong_status_t prepare_letterbox_target(Tensor& dst,
                                                 const std::vector<int64_t>& target_shape,
                                                 std::size_t required_bytes) {
    try {
        switch (letterbox_target_action(dst, target_shape, required_bytes)) {
            case LetterboxTargetAction::reuse:
                break;
            case LetterboxTargetAction::rebind_dma:
                dst = Tensor::from_dma_buf(dst.dma_buf_fd(), target_shape, DataType::UINT8, dst.raw_data());
                break;
            case LetterboxTargetAction::allocate_cpu:
                dst = Tensor(target_shape, DataType::UINT8);
                break;
        }
    } catch (const std::bad_alloc&) {
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    } catch (const std::length_error&) {
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    }

    // 上述三个分支保证 dtype 已是 UINT8，因此 data<uint8_t>() 不会抛 dtype 不匹配；
    // 仍保留空指针防御：容量为 0 的目标形状在这里得到明确的 OUT_OF_MEMORY。
    if (dst.data<uint8_t>() == nullptr) {
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    }
    return ZHULONG_PLUGIN_OK;
}

} // namespace zhulong
