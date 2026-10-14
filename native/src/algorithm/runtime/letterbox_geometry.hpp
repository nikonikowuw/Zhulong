#pragma once

namespace zhulong {

// Letterbox 的纯几何计算：等比缩放比与居中补边量。
//
// 提取理由：CPU 与 RGA 两个视觉后端必须用**同一套**几何语义，否则同一个
// 输入在两个后端下会得到不同的 scale/padding，破坏「后端可替换」的前提。
// 本函数只做算术，不触碰像素、不分配内存、不依赖任何后端。
struct LetterboxGeometry {
    float scale{1.0f};   // 等比缩放系数（源 → 目标）
    int unpad_w{0};      // 缩放后内容宽度（未补边）
    int unpad_h{0};      // 缩放后内容高度（未补边）
    int pad_left{0};     // 左侧补边像素
    int pad_top{0};      // 顶部补边像素
};

// 计算 letterbox 几何。src_w/src_h/target_w/target_h 必须为正。
//
// ⚠️ 本函数**假定**输入已通过参数校验（正尺寸），与 CpuVisionBackend::letterbox
// 在调用前的校验保持一致；它不做错误返回，避免在两个后端里各写一遍校验分支。
LetterboxGeometry compute_letterbox_geometry(int src_w, int src_h, int target_w, int target_h);

} // namespace zhulong
