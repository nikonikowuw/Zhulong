#include "algorithm/runtime/gallery_registry.hpp"

namespace zhulong::runtime {

GalleryRegistry::GalleryRegistry() = default;

GalleryRegistry& GalleryRegistry::instance() {
    static GalleryRegistry registry;
    return registry;
}

void GalleryRegistry::rebuild_snapshot_locked() {
    auto vec = std::make_shared<std::vector<FaceTemplate>>();
    vec->reserve(faces_.size());
    for (const auto& [_, item] : faces_) {
        vec->push_back(item);
    }
    snapshot_ = std::move(vec);
}

void GalleryRegistry::reset(std::vector<FaceTemplate> items) {
    std::lock_guard<std::mutex> lock(mutex_);
    faces_.clear();
    for (auto& item : items) {
        if (!item.face_id.empty()) {
            faces_[item.face_id] = std::move(item);
        }
    }
    rebuild_snapshot_locked();
}

void GalleryRegistry::upsert(FaceTemplate item) {
    if (item.face_id.empty()) {
        return;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    faces_[item.face_id] = std::move(item);
    rebuild_snapshot_locked();
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
    rebuild_snapshot_locked();
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
    std::lock_guard<std::mutex> lock(mutex_);
    faces_.clear();
    snapshot_ = std::make_shared<std::vector<FaceTemplate>>();
}

} // namespace zhulong::runtime
