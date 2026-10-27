/**
 * @file gallery_index.hpp
 * @brief 算法 Runner 的人脸底库索引门面与分片聚合上下文
 *
 * 核心设计原则：
 * 1. **单一数据源**：底库内容与只读快照只存在于进程内的
 *    `zhulong::runtime::GalleryRegistry`。本类不持有任何底库状态。
 *    ⚠️ 本类历史实现同时持有第二份 faces_ + snapshot_：每次写先在自己的锁下做一遍
 *    全量深拷贝，再转发给 GalleryRegistry 做第二遍。后果是底库内存翻倍（每条含 4 个
 *    string + 512 维 float）、每次写两份拷贝，且两份状态一旦在异常路径上分叉就会
 *    永久不一致（两份都各自对外可见，谁对谁错无法判定）。现在它是纯转发，
 *    写路径只做一次重建。
 * 2. 分片聚合状态机：ShardedSyncContext 跟踪 MSG_GALLERY_SYNC 分片收集，
 *    严格校验分片序号、总片数与超时机制（默认 5s），杜绝残缺底库生效。
 */

#pragma once

#include <zhulong/runtime/gallery_registry.hpp>
#include <string>
#include <vector>
#include <memory>
#include <chrono>
#include <set>

namespace zhulong::runner {

using FaceTemplate = zhulong::runtime::FaceTemplate;

/**
 * @brief Runner 的人脸底库写入口门面（无状态，转发到 GalleryRegistry 单例）
 */
class GalleryIndex {
public:
    void reset(std::vector<FaceTemplate> items) {
        zhulong::runtime::GalleryRegistry::instance().reset(std::move(items));
    }

    void upsert(FaceTemplate item) {
        zhulong::runtime::GalleryRegistry::instance().upsert(std::move(item));
    }

    void remove(const std::string& face_id, const std::string& person_id) {
        zhulong::runtime::GalleryRegistry::instance().remove(face_id, person_id);
    }

    [[nodiscard]] size_t size() const { return zhulong::runtime::GalleryRegistry::instance().size(); }

    [[nodiscard]] std::shared_ptr<const std::vector<FaceTemplate>> snapshot() const {
        return zhulong::runtime::GalleryRegistry::instance().snapshot();
    }
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
