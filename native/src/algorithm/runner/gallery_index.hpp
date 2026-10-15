/**
 * @file gallery_index.hpp
 * @brief 算法 Runner 进程内人脸底库索引与分片聚合上下文
 *
 * 核心设计原则：
 * 1. 读写隔离：写操作（全量 reset、增量 upsert / remove）在 mutex 保护下完成，
 *    并更新只读 snapshot_ 指针；工作线程读取 snapshot 零阻塞，绝不争用写锁。
 * 2. 分片聚合状态机：ShardedSyncContext 跟踪 MSG_GALLERY_SYNC 分片收集，
 *    严格校验分片序号、总片数与超时机制（默认 5s），杜绝残缺底库生效。
 */

#pragma once

#include "algorithm/runtime/gallery_registry.hpp"
#include <string>
#include <vector>
#include <unordered_map>
#include <mutex>
#include <memory>
#include <chrono>
#include <set>

namespace zhulong::runner {

using FaceTemplate = zhulong::runtime::FaceTemplate;

/**
 * @brief Runner 进程内并发安全人脸底库索引表
 */
class GalleryIndex {
public:
    void reset(std::vector<FaceTemplate> items) {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            faces_.clear();
            for (auto& item : items) {
                faces_[item.face_id] = item;
            }
            rebuild_snapshot_locked();
        }
        zhulong::runtime::GalleryRegistry::instance().reset(std::move(items));
    }

    void upsert(FaceTemplate item) {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            faces_[item.face_id] = item;
            rebuild_snapshot_locked();
        }
        zhulong::runtime::GalleryRegistry::instance().upsert(std::move(item));
    }

    void remove(const std::string& face_id, const std::string& person_id) {
        {
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
        zhulong::runtime::GalleryRegistry::instance().remove(face_id, person_id);
    }

    [[nodiscard]] size_t size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return faces_.size();
    }

    [[nodiscard]] std::shared_ptr<const std::vector<FaceTemplate>> snapshot() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return snapshot_;
    }

private:
    void rebuild_snapshot_locked() {
        auto snap = std::make_shared<std::vector<FaceTemplate>>();
        snap->reserve(faces_.size());
        for (const auto& [_, t] : faces_) {
            snap->push_back(t);
        }
        snapshot_ = std::move(snap);
    }

    mutable std::mutex mutex_;
    std::unordered_map<std::string, FaceTemplate> faces_;
    std::shared_ptr<const std::vector<FaceTemplate>> snapshot_{std::make_shared<std::vector<FaceTemplate>>()};
};

/**
 * @brief 分片聚合上下文
 */
struct ShardedSyncContext {
    std::string sync_id;
    int shard_total{0};
    std::set<int> received_shards;
    std::vector<FaceTemplate> collected_items;
    std::chrono::steady_clock::time_point start_time;

    void start_sync(const std::string& id, int total) {
        sync_id = id;
        shard_total = total;
        received_shards.clear();
        collected_items.clear();
        start_time = std::chrono::steady_clock::now();
    }

    [[nodiscard]] bool is_expired(std::chrono::milliseconds timeout = std::chrono::milliseconds(5000)) const {
        if (shard_total <= 0) return false;
        return (std::chrono::steady_clock::now() - start_time) > timeout;
    }

    [[nodiscard]] bool is_complete() const {
        return shard_total > 0 && static_cast<int>(received_shards.size()) == shard_total;
    }
};

} // namespace zhulong::runner
