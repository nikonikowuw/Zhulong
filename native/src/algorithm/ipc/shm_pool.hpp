/**
 * @file shm_pool.hpp
 * @brief POSIX 共享内存池（ShmPool）的 RAII 封装与槽位寻址
 *
 * 核心设计原则：
 * 1. 权限与所有权严格分立：
 *    - CREATE_READ_WRITE 模式（宿主进程）：拥有该共享内存段（is_owner_=true），负责创建、截断大小、写入头部并在停机时调用 shm_unlink；
 *    - OPEN_READ_ONLY 模式（Runner 子进程）：仅以 PROT_READ 只读映射（is_owner_=false），绝不释放或删除底层共享内存。
 * 2. 槽位偏移计算与越界防御：slot_write_ptr 与 slot_read_ptr 严格基于 slot_idx < slot_count_ 执行边界检查，
 *    槽位基准偏移恒为 sizeof(ShmHeader) + slot_idx * slot_size。
 */

#pragma once

#include "shm_layout.hpp"
#include <string>
#include <cstdint>
#include <cstddef>
#include <memory>
#include <system_error>

namespace zhulong::ipc {

/**
 * @brief 共享内存映射访问模式
 */
enum class ShmMode {
    CREATE_READ_WRITE, /**< 主进程模式：负责创建段、截断大小、写入元数据头并可读写数据槽位 */
    OPEN_READ_ONLY     /**< 子进程模式：只读打开已有共享内存段，物理禁止修改任何内存 */
};

/**
 * @brief 共享内存池管理类（RAII 封装）
 *
 * 封装 POSIX shm_open、ftruncate、mmap、munmap 与 shm_unlink，
 * 负责共享内存段生命周期管理及单槽位线性地址寻址。
 */
class ShmPool {
public:
    ShmPool() = default;
    ~ShmPool();

    // 禁用拷贝，允许移动
    ShmPool(const ShmPool&) = delete;
    ShmPool& operator=(const ShmPool&) = delete;

    /**
     * @brief 移动构造函数：转移底层 fd 与虚拟地址映射所有权，原对象句柄清空
     */
    ShmPool(ShmPool&& other) noexcept;

    /**
     * @brief 移动赋值运算符：先安全关闭自身既有映射，再接管 other 的共享内存所有权
     */
    ShmPool& operator=(ShmPool&& other) noexcept;

    /**
     * @brief 初始化或连接共享内存池
     *
     * @param name 共享内存段名称（必须以 '/' 开头，如 "/zhulong_shm_1"）
     * @param mode 访问模式（创建读写或只读打开）
     * @param slot_count 槽位数（仅 CREATE_READ_WRITE 模式生效，默认 8）
     * @param slot_size 单个槽位字节大小（仅 CREATE_READ_WRITE 模式生效，默认 4MB）
     * @param[out] error_msg 失败时输出详细系统错误信息（可选）
     * @return true 初始化成功；false 失败（由 error_msg 携带具体原因）
     */
    bool init(const std::string& name, ShmMode mode,
              size_t slot_count = DEFAULT_SLOT_COUNT,
              size_t slot_size = DEFAULT_SLOT_SIZE,
              std::string* error_msg = nullptr);

    /**
     * @brief 关闭共享内存映射并释放文件描述符；若为宿主持有者则执行 shm_unlink
     */
    void close();

    /**
     * @brief 检查共享内存映射指针是否有效
     */
    [[nodiscard]] bool is_valid() const noexcept { return mapped_ptr_ != nullptr; }

    /**
     * @brief 获取共享内存段名称
     */
    [[nodiscard]] const std::string& name() const noexcept { return name_; }

    /**
     * @brief 获取槽位总数
     */
    [[nodiscard]] size_t slot_count() const noexcept { return slot_count_; }

    /**
     * @brief 获取单个槽位字节容量
     */
    [[nodiscard]] size_t slot_size() const noexcept { return slot_size_; }

    /**
     * @brief 获取整段共享内存总映射字节数
     */
    [[nodiscard]] size_t total_size() const noexcept { return total_size_; }

    /**
     * @brief 获取指定槽位的写指针
     *
     * @param slot_idx 目标槽位索引（0 ~ slot_count-1）
     * @return uint8_t* 槽位有效可写起始地址；若非 CREATE_READ_WRITE 模式或越界则返回 nullptr
     */
    uint8_t* slot_write_ptr(size_t slot_idx);

    /**
     * @brief 获取指定槽位的只读借用指针
     *
     * @param slot_idx 目标槽位索引（0 ~ slot_count-1）
     * @return const uint8_t* 槽位有效只读起始地址；若未映射或越界则返回 nullptr
     */
    const uint8_t* slot_read_ptr(size_t slot_idx) const;

private:
    std::string name_;                        /**< POSIX 共享内存对象名称 */
    ShmMode mode_{ShmMode::OPEN_READ_ONLY};   /**< 当前实例的映射模式 */
    int fd_{-1};                              /**< 共享内存文件描述符 */
    uint8_t* mapped_ptr_{nullptr};            /**< mmap 返回的虚拟地址基指针 */
    size_t total_size_{0};                    /**< 总映射字节数 */
    size_t slot_count_{0};                    /**< 槽位总数 */
    size_t slot_size_{0};                     /**< 单个槽位字节数 */
    bool is_owner_{false};                    /**< 是否为该共享内存段的持有者（负责停机时 unlink） */
};

} // namespace zhulong::ipc

