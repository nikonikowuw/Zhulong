/**
 * @file spatial_clustering.hpp
 * @brief 空间透视自适应聚集分析与并查集聚类
 */

#pragma once

#include "detection/person_detector.hpp"

#include <array>
#include <vector>

namespace zhulong::crowd {

/**
 * @brief 聚类生成的聚集簇候选
 */
struct ClusterCandidate {
    std::array<float, 4> enclosing_box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 最小外接包络矩形 [x1, y1, x2, y2] ∈ [0, 1] */
    float centroid_x{0.0f};                                     /**< 质心 X */
    float centroid_y{0.0f};                                     /**< 质心 Y */
    int person_count{0};                                        /**< 簇内人数 */
    float score{0.0f};                                          /**< 簇平均置信度 */
};

class SpatialClusterer {
public:
    /**
     * @brief 对单帧检测到的人员执行透视自适应距离聚类
     *
     * ⚠️ 归一化坐标在 X/Y 上是**两个不同尺度**（x 除以帧宽、y 除以帧高）。直接用
     *    `sqrt(dx² + dy²)` 再除以归一化身高，等价于把横向像素距离乘上 `H/W` ——
     *    16:9 的相机上横向聚集判据会被悄悄放宽 1.78 倍，而纵向仍是原判据。
     *    因此必须传入帧宽高比，把 X 轴折算回与 Y 轴同尺度的像素空间。
     *
     * @param persons 单帧检出人员列表（归一化框）
     * @param distance_ratio 相对人体身高的空间聚集距离比值
     * @param count_threshold 聚集最小人数阈值
     * @param aspect_ratio 帧宽高比（`width / height`），必须为正
     * @return 达到人数阈值的聚集簇候选列表（按人数降序、质心升序稳定排序）
     */
    static std::vector<ClusterCandidate> cluster(
        const std::vector<DetectedPerson>& persons,
        float distance_ratio,
        int count_threshold,
        float aspect_ratio
    );
};

} // namespace zhulong::crowd
