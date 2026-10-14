#include "letterbox_geometry.hpp"

#include <algorithm>
#include <cmath>

namespace zhulong {

LetterboxGeometry compute_letterbox_geometry(int src_w, int src_h, int target_w, int target_h) {
    // ⚠️ 本函数体是从 CpuVisionBackend::letterbox 逐行提取的，**算术必须保持逐位一致**：
    // 提取前的 CPU 输出已被既有测试（algo_runtime_test.cpp 的 letterbox 断言）固化，
    // 任何精度/舍入改变都会让那些断言失败。
    LetterboxGeometry geometry;

    const float r = std::min(static_cast<float>(target_w) / static_cast<float>(src_w),
                             static_cast<float>(target_h) / static_cast<float>(src_h));

    const int unpad_w = static_cast<int>(std::clamp(std::llround(static_cast<double>(src_w) * r),
                                                    1LL, static_cast<long long>(target_w)));
    const int unpad_h = static_cast<int>(std::clamp(std::llround(static_cast<double>(src_h) * r),
                                                    1LL, static_cast<long long>(target_h)));

    const int dw = target_w - unpad_w;
    const int dh = target_h - unpad_h;

    geometry.scale = r;
    geometry.unpad_w = unpad_w;
    geometry.unpad_h = unpad_h;
    geometry.pad_left = dw / 2;
    geometry.pad_top = dh / 2;
    return geometry;
}

} // namespace zhulong
