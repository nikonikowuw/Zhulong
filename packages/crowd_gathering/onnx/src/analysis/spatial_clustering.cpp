/**
 * @file spatial_clustering.cpp
 * @brief 空间透视自适应聚集分析与并查集聚类实现
 */

#include "spatial_clustering.hpp"

#include <algorithm>
#include <cmath>
#include <numeric>
#include <unordered_map>

namespace zhulong::crowd {

namespace {

struct DisjointSet {
    std::vector<int> parent;
    std::vector<int> rank;

    explicit DisjointSet(size_t n) : parent(n), rank(n, 0) {
        std::iota(parent.begin(), parent.end(), 0);
    }

    int find(int i) {
        if (parent[i] == i) {
            return i;
        }
        return parent[i] = find(parent[i]);
    }

    void unite(int i, int j) {
        int root_i = find(i);
        int root_j = find(j);
        if (root_i != root_j) {
            if (rank[root_i] < rank[root_j]) {
                std::swap(root_i, root_j);
            }
            parent[root_j] = root_i;
            if (rank[root_i] == rank[root_j]) {
                rank[root_i]++;
            }
        }
    }
};

struct PersonGeometry {
    float foot_x{0.0f};
    float foot_y{0.0f};
    float height{0.0f};
};

} // namespace

std::vector<ClusterCandidate> SpatialClusterer::cluster(
    const std::vector<DetectedPerson>& persons,
    float distance_ratio,
    int count_threshold
) {
    const size_t n = persons.size();
    if (n < static_cast<size_t>(count_threshold) || count_threshold <= 1) {
        return {};
    }

    std::vector<PersonGeometry> geoms(n);
    for (size_t i = 0; i < n; ++i) {
        const auto& box = persons[i].box;
        geoms[i].foot_x = (box[0] + box[2]) * 0.5f;
        geoms[i].foot_y = box[3]; // 框底边中点作为人体接地点
        geoms[i].height = std::max(0.001f, box[3] - box[1]);
    }

    DisjointSet dsu(n);

    // 两两计算透视自适应相对距离
    for (size_t i = 0; i < n; ++i) {
        for (size_t j = i + 1; j < n; ++j) {
            const float dx = geoms[i].foot_x - geoms[j].foot_x;
            const float dy = geoms[i].foot_y - geoms[j].foot_y;
            const float dist = std::sqrt(dx * dx + dy * dy);
            const float avg_height = (geoms[i].height + geoms[j].height) * 0.5f;

            if (avg_height > 1e-4f) {
                const float rel_dist = dist / avg_height;
                if (rel_dist <= distance_ratio) {
                    dsu.unite(static_cast<int>(i), static_cast<int>(j));
                }
            }
        }
    }

    // 按连通根节点归约簇成员
    std::unordered_map<int, std::vector<size_t>> clusters;
    for (size_t i = 0; i < n; ++i) {
        const int root = dsu.find(static_cast<int>(i));
        clusters[root].push_back(i);
    }

    std::vector<ClusterCandidate> candidates;
    for (const auto& [root, member_indices] : clusters) {
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

    return candidates;
}

} // namespace zhulong::crowd
