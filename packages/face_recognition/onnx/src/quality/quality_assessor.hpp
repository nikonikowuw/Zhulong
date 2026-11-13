/**
 * @file quality_assessor.hpp
 * @brief 基于 eDifFIQA(T) 的轻量级对齐人脸质量评估器
 *
 * 输入为对齐后的 112×112 RGB24 紧凑人脸块（与 FaceLiVTv2-L 共用同一对齐输出），
 * 推理产出 [0.0, 1.0] 归一化标量质量分（越高表示清晰度/姿态/光照/无遮挡综合质量越好）。
 */

#pragma once

#include <zhulong/algo.hpp>

namespace zhulong::face {

/** @brief eDifFIQA(T) 输入裁剪块尺寸（与 ArcFace 112×112 对齐模板一致） */
inline constexpr int kQualityPatchWidth = 112;
inline constexpr int kQualityPatchHeight = 112;

class FaceQualityAssessor {
public:
    FaceQualityAssessor() = default;

    /**
     * @brief 使用指定推理会话对 112×112 RGB24 对齐人脸块进行质量评估
     *
     * @param session eDifFIQA(T) 会话句柄
     * @param patch_tensor {1, 112, 112, 3} UINT8 RGB24 对齐脸块
     * @param out_score 输出质量评分，已校验有限性并钳制到 [0.0, 1.0]
     * @return true 推理成功并产出有效质量分；false 会话/张量/输出异常
     */
    bool assess(
        zhulong::Session& session,
        const zhulong::Tensor& patch_tensor,
        float& out_score
    ) const;
};

} // namespace zhulong::face
