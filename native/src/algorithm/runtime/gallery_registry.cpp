#include <zhulong/runtime/gallery_registry.hpp>

#include <algorithm>
#include <utility>

namespace zhulong::runtime {

namespace {

void sort_snapshot(std::vector<FaceTemplate>& vec) {
    // ⚠️ unordered_map 的迭代顺序取决于插入顺序与哈希布局，进程间不保证一致。
    //    快照顺序若不固定，任何「按索引缓存 / 做 A-B 对比 / 断言首条」的下游都会漂移。
    //    按 face_id 排序把顺序固化成内容函数，与插入顺序解耦。
    std::sort(vec.begin(), vec.end(), [](const FaceTemplate& a, const FaceTemplate& b) {
        return a.face_id < b.face_id;
    });
}

} // namespace

GalleryRegistry::GalleryRegistry() = default;

GalleryRegistry& GalleryRegistry::instance() {
    static GalleryRegistry registry;
    return registry;
}

void GalleryRegistry::rebuild_snapshot_locked() {
    auto vec = std::make_shared<std::vector<FaceTemplate>>();
    vec->reserve(faces_.size());
    for (const auto& entry : faces_) {
        vec->push_back(entry.second);
    }
    sort_snapshot(*vec);
    snapshot_ = std::move(vec);
}

void GalleryRegistry::reset(std::vector<FaceTemplate> items) {
    std::unordered_map<std::string, FaceTemplate> next_faces;
    next_faces.reserve(items.size());
    for (auto& item : items) {
        if (!item.face_id.empty()) {
            next_faces[item.face_id] = std::move(item);
        }
    }
    auto next_snapshot = std::make_shared<std::vector<FaceTemplate>>();
    next_snapshot->reserve(next_faces.size());
    for (const auto& entry : next_faces) {
        next_snapshot->push_back(entry.second);
    }
    sort_snapshot(*next_snapshot);

    std::lock_guard<std::mutex> lock(mutex_);
    faces_.swap(next_faces);
    snapshot_ = std::move(next_snapshot);
}

void GalleryRegistry::upsert(FaceTemplate item) {
    if (item.face_id.empty()) {
        return;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    // 先完成所有可能抛 std::bad_alloc 的分配（目标桶扩容、节点分配与快照构建），
    // 最后以不抛异常的步骤提交，确保 faces_ 与 snapshot_ 强异常安全一致。
    const bool exists = faces_.find(item.face_id) != faces_.end();
    if (!exists) {
        faces_.reserve(faces_.size() + 1);
    }

    auto next_snapshot = std::make_shared<std::vector<FaceTemplate>>();
    next_snapshot->reserve(faces_.size() + (exists ? 0 : 1));
    for (const auto& entry : faces_) {
        if (entry.first != item.face_id) {
            next_snapshot->push_back(entry.second);
        }
    }
    next_snapshot->push_back(item);
    sort_snapshot(*next_snapshot);

    std::unordered_map<std::string, FaceTemplate> staged;
    staged.emplace(item.face_id, std::move(item));
    auto node = staged.extract(staged.begin());

    auto it = faces_.find(node.key());
    if (it != faces_.end()) {
        it->second = std::move(node.mapped());
    } else {
        faces_.insert(std::move(node));
    }
    snapshot_ = std::move(next_snapshot);
}

void GalleryRegistry::remove(const std::string& face_id, const std::string& person_id) {
    if (face_id.empty() && person_id.empty()) {
        return;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    auto next_snapshot = std::make_shared<std::vector<FaceTemplate>>();
    next_snapshot->reserve(faces_.size());
    bool changed = false;
    for (const auto& entry : faces_) {
        const bool match = !face_id.empty() ? (entry.first == face_id)
                                            : (entry.second.person_id == person_id);
        if (match) {
            changed = true;
        } else {
            next_snapshot->push_back(entry.second);
        }
    }
    if (!changed) {
        return;
    }
    sort_snapshot(*next_snapshot);

    if (!face_id.empty()) {
        faces_.erase(face_id);
    } else {
        for (auto it = faces_.begin(); it != faces_.end();) {
            if (it->second.person_id == person_id) {
                it = faces_.erase(it);
            } else {
                ++it;
            }
        }
    }
    snapshot_ = std::move(next_snapshot);
}

std::shared_ptr<const std::vector<FaceTemplate>> GalleryRegistry::snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return snapshot_;
}

size_t GalleryRegistry::size() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return faces_.size();
}

void GalleryRegistry::clear() {
    auto empty_snapshot = std::make_shared<std::vector<FaceTemplate>>();
    std::lock_guard<std::mutex> lock(mutex_);
    faces_.clear();
    snapshot_ = std::move(empty_snapshot);
}

} // namespace zhulong::runtime
