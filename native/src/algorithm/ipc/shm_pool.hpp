#pragma once

#include "shm_layout.hpp"
#include <string>
#include <cstdint>
#include <cstddef>
#include <memory>
#include <system_error>

namespace zhulong::ipc {

enum class ShmMode {
    CREATE_READ_WRITE, // 主进程模式：创建并可读写
    OPEN_READ_ONLY     // 子进程模式：只读映射
};

class ShmPool {
public:
    ShmPool() = default;
    ~ShmPool();

    // 禁用拷贝，允许移动
    ShmPool(const ShmPool&) = delete;
    ShmPool& operator=(const ShmPool&) = delete;
    ShmPool(ShmPool&& other) noexcept;
    ShmPool& operator=(ShmPool&& other) noexcept;

    /**
     * @brief 初始化或连接共享内存池
     * @param name 共享内存段名称 (以 / 开头)
     * @param mode 创建读写或只读打开
     * @param slot_count 槽位数 (CREATE 模式有效)
     * @param slot_size 单个槽位大小 (CREATE 模式有效)
     */
    bool init(const std::string& name, ShmMode mode,
              size_t slot_count = DEFAULT_SLOT_COUNT,
              size_t slot_size = DEFAULT_SLOT_SIZE,
              std::string* error_msg = nullptr);

    void close();

    [[nodiscard]] bool is_valid() const noexcept { return mapped_ptr_ != nullptr; }
    [[nodiscard]] const std::string& name() const noexcept { return name_; }
    [[nodiscard]] size_t slot_count() const noexcept { return slot_count_; }
    [[nodiscard]] size_t slot_size() const noexcept { return slot_size_; }
    [[nodiscard]] size_t total_size() const noexcept { return total_size_; }

    /**
     * @brief 获取指定槽位的写指针 (仅 CREATE_READ_WRITE 模式有效)
     */
    uint8_t* slot_write_ptr(size_t slot_idx);

    /**
     * @brief 获取指定槽位的只读指针
     */
    const uint8_t* slot_read_ptr(size_t slot_idx) const;

private:
    std::string name_;
    ShmMode mode_{ShmMode::OPEN_READ_ONLY};
    int fd_{-1};
    uint8_t* mapped_ptr_{nullptr};
    size_t total_size_{0};
    size_t slot_count_{0};
    size_t slot_size_{0};
    bool is_owner_{false};
};

} // namespace zhulong::ipc
