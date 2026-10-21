/**
 * @file broker_resource_lock.hpp
 * @brief 与 Go 宿主启动清扫共享的 broker 资源目录锁（advisory flock）
 *
 * 问题背景：FrameBroker::start 创建的两类资源都存在「文件已出现但尚不可用」的窗口：
 *  - /dev/shm：shm_open 先发布名字，之后才 mmap；此时「按路径查 maps」的清扫器会认定无人使用。
 *  - socket：bind() 先发布 socket inode，之后才 listen()；此时 connect 探测返回 ECONNREFUSED，
 *    与废弃 socket 完全同形。
 *
 * 因此宿主启动清扫（Go: internal/algorunner）与 broker 创建（本文件）对同一批目录持有
 * 排他 advisory flock：
 *  - 创建侧阻塞取锁，保证 shm 初始化与 socket bind/listen 期间无人清扫；
 *  - 清扫侧非阻塞取锁，拿到锁才允许 unlink，锁忙则整体跳过（资源保留，启动继续）。
 *
 * ⚠️ 锁是协作式约定：只有双方锁定同一批目录对象才成立。两侧都把目录集合按
 *    (device, inode) 去重并升序排序，因此别名路径（symlink、bind mount、base_dir 就是
 *    /dev/shm）不会产生两把锁，也不会出现交叉加锁顺序导致的死锁。
 */

#pragma once

#include <memory>
#include <string>
#include <vector>

namespace zhulong::ipc {

/**
 * @brief 一组已持有的 broker 资源目录锁（RAII）
 *
 * 构造即加锁成功，析构自动解锁。不可拷贝，只能由 acquire() 产生。
 */
class BrokerResourceLock {
public:
    /**
     * @brief 对 dirs 中的目录取排他 flock
     *
     * @param dirs 需要互斥的目录集合（内部去重排序）
     * @param[out] error_msg 失败原因
     * @return 成功返回非空持有对象；失败返回 nullptr
     */
    static std::unique_ptr<BrokerResourceLock> acquire(const std::vector<std::string>& dirs,
                                                       std::string* error_msg = nullptr);

    ~BrokerResourceLock();

    BrokerResourceLock(const BrokerResourceLock&) = delete;
    BrokerResourceLock& operator=(const BrokerResourceLock&) = delete;
    BrokerResourceLock(BrokerResourceLock&&) = delete;
    BrokerResourceLock& operator=(BrokerResourceLock&&) = delete;

    /** @brief 当前持有的锁对象数量（去重后的目录数），用于诊断与测试 */
    [[nodiscard]] size_t size() const noexcept { return fds_.size(); }

    /**
     * @brief broker 资源目录集合（创建侧与测试的唯一来源）
     *
     * 创建侧（FrameBroker::start）与宿主清扫（Go: internal/algorunner）必须锁定同一批对象，
     * 否则互斥不成立。这里集中定义避免调用点各写一份字面量而漂移。
     *
     * shm 目录只在存在可枚举挂载点的平台上纳入：Linux 的 shm_open 名字空间是 /dev/shm，
     * macOS 在内存里且 Go 侧清扫为 no-op（orphan_other.go），锁一个不存在的目录只会让
     * stat 失败、把 broker 创建整体打成 ERR_IO。
     *
     * @param base_dir broker socket 所在目录
     */
    static std::vector<std::string> resource_dirs(const std::string& base_dir);

private:
    BrokerResourceLock() = default;

    std::vector<int> fds_;
};

} // namespace zhulong::ipc
