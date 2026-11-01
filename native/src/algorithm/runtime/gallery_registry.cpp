#include <zhulong/runtime/gallery_registry.hpp>

#include <algorithm>

namespace zhulong::runtime {

GalleryRegistry::GalleryRegistry() = default;

GalleryRegistry& GalleryRegistry::instance() {
    static GalleryRegistry registry;
    return registry;
}

void GalleryRegistry::rebuild_snapshot_locked() const {
    auto vec = std::make_shared<std::vector<FaceTemplate>>();
    vec->reserve(faces_.size());
    for (const auto& entry : faces_) {
        vec->push_back(entry.second);
    }
    // ⚠️ unordered_map 的迭代顺序取决于插入顺序与哈希布局，进程间不保证一致。
    //    快照顺序若不固定，任何「按索引缓存 / 做 A-B 对比 / 断言首条」的下游都会漂移。
    //    按 face_id 排序把顺序固化成内容函数，与插入顺序解耦。
    std::sort(vec->begin(), vec->end(), [](const FaceTemplate& a, const FaceTemplate& b) {
        return a.face_id < b.face_id;
    });
    snapshot_ = std::move(vec);
    dirty_ = false;
}

void GalleryRegistry::reset(std::vector<FaceTemplate> items) {
    std::lock_guard<std::mutex> lock(mutex_);
    faces_.clear();
    for (auto& item : items) {
        if (!item.face_id.empty()) {
            faces_[item.face_id] = std::move(item);
        }
    }
    dirty_ = true;
}

void GalleryRegistry::upsert(FaceTemplate item) {
    if (item.face_id.empty()) {
        return;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    faces_[item.face_id] = std::move(item);
    dirty_ = true;
}

void GalleryRegistry::remove(const std::string& face_id, const std::string& person_id) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!face_id.empty()) {
        faces_.erase(face_id);
    } else if (!person_id.empty()) {
        for (auto it = faces_.begin(); it != faces_.end();) {
            if (it->second.person_id == person_id) {
                it = faces_.erase(it);
            } else {
                ++it;
            }
        }
    }
    dirty_ = true;
}

std::shared_ptr<const std::vector<FaceTemplate>> GalleryRegistry::snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    if (dirty_) {
        rebuild_snapshot_locked();
    }
    return snapshot_;
}

size_t GalleryRegistry::size() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return faces_.size();
}

void GalleryRegistry::clear() {
    std::lock_guard<std::mutex> lock(mutex_);
    faces_.clear();
    snapshot_ = std::make_shared<std::vector<FaceTemplate>>();
    dirty_ = false;
}

} // namespace zhulong::runtime
