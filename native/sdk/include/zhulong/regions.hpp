#pragma once

#include <array>
#include <cstdint>
#include <string_view>

namespace zhulong {

/** Host-owned full-frame region JSON, borrowed for this inference call only.
 * Parse tripwires once per revision with your algorithm's JSON library; copy
 * any data retained across calls. revision is local to this worker lifetime.
 */
struct TaskRegions {
    std::string_view json{};
    uint64_t revision{0};
};

/** Actual pixel-aligned input window. Points outside the crop remain outside
 * [0,1]; do not clamp them (clamping changes the geometry of a tripwire).
 */
struct InputView {
    std::array<float, 4> rect{0, 0, 1, 1};
    bool known{false}; /**< Old hosts omit this metadata; do not assume full frame. */

    std::array<double, 2> to_input(double x, double y) const noexcept {
        return {(x - rect[0]) / (rect[2] - rect[0]),
                (y - rect[1]) / (rect[3] - rect[1])};
    }
    std::array<double, 2> to_full_frame(double x, double y) const noexcept {
        return {rect[0] + x * (rect[2] - rect[0]),
                rect[1] + y * (rect[3] - rect[1])};
    }
};

} // namespace zhulong
