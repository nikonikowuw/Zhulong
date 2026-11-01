/**
 * @file gallery_registry.hpp
 * @brief 进程内线程安全的人脸底库注册表（快照式读写隔离）
 *
 * 核心设计原则：
 * 1. 进程内单例，**不是**跨镜像共享内存：实例归属链接产物，不归属进程名。
 *    交付形态下本库以 `.a` 静态链接进算法包（见 runtime_factory.hpp），因此每个链接了
 *    本静态库的可执行文件/动态库各自拿到**独立一份** GalleryRegistry。只有两个镜像都
 *    动态链接同一个 libZhulongAlgoRuntime.so 时，它们才真正共享同一份视图。
 *    ⚠️ 不要假设 Runner 与插件会天然看到同一份底库 —— 底库跨进程一致靠 Go 侧的
 *    MSG_GALLERY_SYNC 广播，不靠这个单例。
 * 2. 读写隔离：写操作（reset, upsert, remove）在 mutex 保护下原子更新 snapshot_ 指针；
 *    推理工作线程调用 snapshot() 获取只读智能指针，零阻塞，无锁争用。
 * 3. 内存所有权明确：底库特征为紧凑 std::vector<float>（512 维），不跨 CGO 泄漏裸指针。
 * 4. 快照顺序确定：按 face_id 升序输出，与插入顺序解耦（见 rebuild_snapshot_locked）。
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
    /**
     * @brief 清空底库并将快照替换为空数组
     */
    void clear();

private:
    GalleryRegistry();
    ~GalleryRegistry() = default;
    GalleryRegistry(const GalleryRegistry&) = delete;
    GalleryRegistry& operator=(const GalleryRegistry&) = delete;

    void rebuild_snapshot_locked() const;

    mutable std::mutex mutex_;
    mutable bool dirty_{false};
    std::unordered_map<std::string, FaceTemplate> faces_;
    mutable std::shared_ptr<const std::vector<FaceTemplate>> snapshot_{std::make_shared<std::vector<FaceTemplate>>()};
};

} // namespace zhulong::runtime
