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
     * @param persons 单帧检出人员列表
     * @param distance_ratio 相对人体身高的空间聚集距离比值
     * @param count_threshold 聚集最小人数阈值
     * @return 达到人数阈值的聚集簇候选列表
     */
    static std::vector<ClusterCandidate> cluster(
        const std::vector<DetectedPerson>& persons,
        float distance_ratio,
        int count_threshold
    );
};

} // namespace zhulong::crowd
