/**
 * @file gallery_registry.hpp
 * @brief 进程内跨动态库共享的人脸底库注册表
 *
 * 核心设计原则：
 * 1. 跨库单例：由 libZhulongAlgoRuntime.so 导出，Runner 进程与 Plugin 动态库共享同一份底库视图。
 * 2. 读写隔离：写操作（reset, upsert, remove）在 mutex 保护下原子更新 snapshot_ 指针；
 *    推理工作线程调用 snapshot() 获取只读智能指针，零阻塞，无锁争用。
 * 3. 内存所有权明确：底库特征为紧凑 std::vector<float>（512 维），不跨 CGO 泄漏裸指针。
 */

#pragma once

#include <string>
#include <vector>
#include <unordered_map>
#include <mutex>
#include <memory>
#include <cstddef>

namespace zhulong::runtime {

/**
 * @brief 人脸底库模板条目
 */
struct FaceTemplate {
    std::string face_id;
    std::string person_id;
    std::string person_name;
    std::string image_path;
    std::vector<float> features; // 512 维归一化特征向量
};

/**
 * @brief 全局线程安全底库注册表
 */
class GalleryRegistry {
public:
    static GalleryRegistry& instance();

    void reset(std::vector<FaceTemplate> items);
    void upsert(FaceTemplate item);
    void remove(const std::string& face_id, const std::string& person_id);

    [[nodiscard]] std::shared_ptr<const std::vector<FaceTemplate>> snapshot() const;
    [[nodiscard]] size_t size() const;
    void clear();

private:
    GalleryRegistry();
    ~GalleryRegistry() = default;
    GalleryRegistry(const GalleryRegistry&) = delete;
    GalleryRegistry& operator=(const GalleryRegistry&) = delete;

    void rebuild_snapshot_locked();

    mutable std::mutex mutex_;
    std::unordered_map<std::string, FaceTemplate> faces_;
    std::shared_ptr<const std::vector<FaceTemplate>> snapshot_{std::make_shared<std::vector<FaceTemplate>>()};
};

} // namespace zhulong::runtime
