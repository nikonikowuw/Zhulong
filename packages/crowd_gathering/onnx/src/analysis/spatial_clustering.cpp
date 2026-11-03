/**
 * @file spatial_clustering.cpp
 * @brief 空间透视自适应聚集分析与并查集聚类实现
 */

#include "spatial_clustering.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <numeric>
#include <unordered_map>

namespace zhulong::crowd {

namespace {

/** @brief 身高下界：防止除零，也把「检出框只有一条线」的病态样本挡在几何计算外 */
constexpr float kMinimumPersonHeight = 1e-3f;
/** @brief 帧宽高比下界：取不到合法宽高时按 1:1 处理（等比，行为可预测） */
constexpr float kMinimumAspectRatio = 1e-3f;

struct DisjointSet {
    std::vector<size_t> parent;
    std::vector<size_t> rank;

    explicit DisjointSet(size_t n) : parent(n), rank(n, 0) {
        std::iota(parent.begin(), parent.end(), 0);
    }

    /** @brief 带路径压缩的迭代查找（递归版在深链上有栈风险，这里刻意不递归） */
    size_t find(size_t index) {
        size_t root = index;
        while (parent[root] != root) {
            root = parent[root];
        }
        while (parent[index] != root) {
            const size_t next = parent[index];
            parent[index] = root;
            index = next;
        }
        return root;
    }

    void unite(size_t a, size_t b) {
        size_t root_a = find(a);
        size_t root_b = find(b);
        if (root_a == root_b) {
            return;
        }
        if (rank[root_a] < rank[root_b]) {
            std::swap(root_a, root_b);
        }
        parent[root_b] = root_a;
        if (rank[root_a] == rank[root_b]) {
            ++rank[root_a];
        }
    }
};

struct PersonGeometry {
    float foot_x{0.0f};  /**< 接地点 X（归一化，已按 aspect 折算） */
    float foot_y{0.0f};  /**< 接地点 Y（归一化） */
    float height{0.0f};  /**< 归一化身高 */
};

} // namespace

std::vector<ClusterCandidate> SpatialClusterer::cluster(
    const std::vector<DetectedPerson>& persons,
    float distance_ratio,
    int count_threshold,
    float aspect_ratio
) {
    if (count_threshold <= 1) {
        return {};
    }
    const size_t n = persons.size();
    if (n < static_cast<size_t>(count_threshold)) {
        return {};
    }
    if (!(distance_ratio > 0.0f)) {
        return {};
    }
    // 宽高比非法时按等比处理：宁可给出可预测的结果，也不要 NaN 距离把所有簇粘成一坨
    const float aspect = (std::isfinite(aspect_ratio) && aspect_ratio > kMinimumAspectRatio) ? aspect_ratio : 1.0f;

    std::vector<PersonGeometry> geoms(n);
    for (size_t i = 0; i < n; ++i) {
        const auto& box = persons[i].box;
        // X 轴折算到 Y 轴的尺度：之后 `dx * dx + dy * dy` 才与像素空间等价
        geoms[i].foot_x = (box[0] + box[2]) * 0.5f * aspect;
        geoms[i].foot_y = box[3]; // 框底边中点作为人体接地点
        geoms[i].height = std::max(kMinimumPersonHeight, box[3] - box[1]);
    }

    DisjointSet dsu(n);

    // 两两计算透视自适应相对距离
    for (size_t i = 0; i < n; ++i) {
        for (size_t j = i + 1; j < n; ++j) {
            const float dx = geoms[i].foot_x - geoms[j].foot_x;
            const float dy = geoms[i].foot_y - geoms[j].foot_y;
            const float dist = std::sqrt(dx * dx + dy * dy);
            const float avg_height = (geoms[i].height + geoms[j].height) * 0.5f;
            const float rel_dist = dist / avg_height;
            if (rel_dist <= distance_ratio) {
                dsu.unite(i, j);
            }
        }
    }

    // 按连通根节点归约簇成员
    std::unordered_map<size_t, std::vector<size_t>> clusters;
    clusters.reserve(n);
    for (size_t i = 0; i < n; ++i) {
        clusters[dsu.find(i)].push_back(i);
    }

    std::vector<ClusterCandidate> candidates;
    candidates.reserve(clusters.size());
    for (const auto& entry : clusters) {
        const std::vector<size_t>& member_indices = entry.second;
        if (member_indices.size() < static_cast<size_t>(count_threshold)) {
            continue;
        }

        ClusterCandidate cand;
        cand.person_count = static_cast<int>(member_indices.size());

        float min_x = 1.0f;
        float min_y = 1.0f;
        float max_x = 0.0f;
        float max_y = 0.0f;
        float sum_score = 0.0f;

        for (const size_t idx : member_indices) {
            const auto& box = persons[idx].box;
            min_x = std::min(min_x, box[0]);
            min_y = std::min(min_y, box[1]);
            max_x = std::max(max_x, box[2]);
            max_y = std::max(max_y, box[3]);
            sum_score += persons[idx].score;
        }

        cand.enclosing_box = {
            std::clamp(min_x, 0.0f, 1.0f),
            std::clamp(min_y, 0.0f, 1.0f),
            std::clamp(max_x, 0.0f, 1.0f),
            std::clamp(max_y, 0.0f, 1.0f)
        };
        cand.centroid_x = (cand.enclosing_box[0] + cand.enclosing_box[2]) * 0.5f;
        cand.centroid_y = (cand.enclosing_box[1] + cand.enclosing_box[3]) * 0.5f;
        cand.score = sum_score / static_cast<float>(member_indices.size());

        candidates.push_back(cand);
    }

    // 无序容器不给顺序：按「人数降序 → 质心 Y 升序 → 质心 X 升序」定序，
    // 否则同一帧的分析结果在不同进程/不同编译器上会跳来跳去（时序匹配与单测都受害）。
    std::sort(candidates.begin(), candidates.end(), [](const ClusterCandidate& a, const ClusterCandidate& b) {
        if (a.person_count != b.person_count) {
            return a.person_count > b.person_count;
        }
        if (a.centroid_y != b.centroid_y) {
            return a.centroid_y < b.centroid_y;
        }
        return a.centroid_x < b.centroid_x;
    });

    return candidates;
}

} // namespace zhulong::crowd
