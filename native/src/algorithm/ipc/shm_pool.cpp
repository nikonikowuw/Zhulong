/**
 * @file shm_pool.cpp
 * @brief 共享内存池生命周期与槽位映射实现
 */

#include "shm_pool.hpp"
#include <sys/mman.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <cstring>
#include <cerrno>

namespace zhulong::ipc {

ShmPool::~ShmPool() {
    close();
}

ShmPool::ShmPool(ShmPool&& other) noexcept
    : name_(std::move(other.name_)),
      mode_(other.mode_),
      fd_(other.fd_),
      mapped_ptr_(other.mapped_ptr_),
      total_size_(other.total_size_),
      slot_count_(other.slot_count_),
      slot_size_(other.slot_size_),
      is_owner_(other.is_owner_) {
    other.fd_ = -1;
    other.mapped_ptr_ = nullptr;
    other.is_owner_ = false;
}

ShmPool& ShmPool::operator=(ShmPool&& other) noexcept {
    if (this != &other) {
        close();
        name_ = std::move(other.name_);
        mode_ = other.mode_;
        fd_ = other.fd_;
        mapped_ptr_ = other.mapped_ptr_;
        total_size_ = other.total_size_;
        slot_count_ = other.slot_count_;
        slot_size_ = other.slot_size_;
        is_owner_ = other.is_owner_;

        other.fd_ = -1;
        other.mapped_ptr_ = nullptr;
        other.is_owner_ = false;
    }
    return *this;
}

bool ShmPool::init(const std::string& name, ShmMode mode,
                   size_t slot_count, size_t slot_size,
                   std::string* error_msg) {
    close();
    name_ = name;
    mode_ = mode;

    if (mode == ShmMode::CREATE_READ_WRITE) {
        // 主进程创建模式：独占拥有该段，预先 unlink 清理可能残留的同名旧段
        is_owner_ = true;
        slot_count_ = slot_count;
        slot_size_ = slot_size;
        total_size_ = sizeof(ShmHeader) + slot_count_ * slot_size_;

        // 预防残留，先 unlink
        shm_unlink(name_.c_str());

        fd_ = shm_open(name_.c_str(), O_CREAT | O_RDWR | O_TRUNC, 0666);
        if (fd_ < 0) {
            if (error_msg) *error_msg = std::string("shm_open create failed: ") + std::strerror(errno);
            return false;
        }

        // 截断文件为总尺寸（头部 + 全部槽位）
        if (ftruncate(fd_, static_cast<off_t>(total_size_)) != 0) {
            if (error_msg) *error_msg = std::string("ftruncate failed: ") + std::strerror(errno);
            close();
            return false;
        }

        // 读写共享映射
        void* ptr = mmap(nullptr, total_size_, PROT_READ | PROT_WRITE, MAP_SHARED, fd_, 0);
        if (ptr == MAP_FAILED) {
            if (error_msg) *error_msg = std::string("mmap failed: ") + std::strerror(errno);
            close();
            return false;
        }
        mapped_ptr_ = static_cast<uint8_t*>(ptr);

        // 初始化头部元数据
        auto* header = reinterpret_cast<ShmHeader*>(mapped_ptr_);
        std::memset(header, 0, sizeof(ShmHeader));
        header->magic = SHM_MAGIC;
        header->version = SHM_VERSION;
        header->slot_count = static_cast<uint32_t>(slot_count_);
        header->slot_size = static_cast<uint32_t>(slot_size_);
        header->total_size = total_size_;

    } else { // ShmMode::OPEN_READ_ONLY
        // 子进程只读连接模式：不拥有该段，绝不执行 unlink
        is_owner_ = false;
        fd_ = shm_open(name_.c_str(), O_RDONLY, 0);
        if (fd_ < 0) {
            if (error_msg) *error_msg = std::string("shm_open read failed: ") + std::strerror(errno);
            return false;
        }

        // 获取实际物理文件大小并做下限检查
        struct stat st{};
        if (fstat(fd_, &st) != 0 || st.st_size < static_cast<off_t>(sizeof(ShmHeader))) {
            if (error_msg) *error_msg = "fstat failed or shm file too small";
            close();
            return false;
        }
        total_size_ = static_cast<size_t>(st.st_size);

        // 以 PROT_READ 严格只读权限建立映射，防止算法子进程污染画面
        void* ptr = mmap(nullptr, total_size_, PROT_READ, MAP_SHARED, fd_, 0);
        if (ptr == MAP_FAILED) {
            if (error_msg) *error_msg = std::string("mmap read failed: ") + std::strerror(errno);
            close();
            return false;
        }
        mapped_ptr_ = static_cast<uint8_t*>(ptr);

        // 校验共享内存头部魔数与协议版本
        auto* header = reinterpret_cast<const ShmHeader*>(mapped_ptr_);
        if (header->magic != SHM_MAGIC || header->version != SHM_VERSION) {
            if (error_msg) *error_msg = "invalid shm header magic or version";
            close();
            return false;
        }

        slot_count_ = header->slot_count;
        slot_size_ = header->slot_size;
    }

    return true;
}

void ShmPool::close() {
    if (mapped_ptr_) {
        munmap(mapped_ptr_, total_size_);
        mapped_ptr_ = nullptr;
    }
    if (fd_ >= 0) {
        ::close(fd_);
        fd_ = -1;
    }
    // 仅宿主持有者在清理时负责从系统全局命名空间删除共享内存对象
    if (is_owner_ && !name_.empty()) {
        shm_unlink(name_.c_str());
        is_owner_ = false;
    }
    total_size_ = 0;
    slot_count_ = 0;
    slot_size_ = 0;
}

uint8_t* ShmPool::slot_write_ptr(size_t slot_idx) {
    if (!mapped_ptr_ || mode_ != ShmMode::CREATE_READ_WRITE || slot_idx >= slot_count_) {
        return nullptr;
    }
    return mapped_ptr_ + sizeof(ShmHeader) + (slot_idx * slot_size_);
}

const uint8_t* ShmPool::slot_read_ptr(size_t slot_idx) const {
    if (!mapped_ptr_ || slot_idx >= slot_count_) {
        return nullptr;
    }
    return mapped_ptr_ + sizeof(ShmHeader) + (slot_idx * slot_size_);
}

} // namespace zhulong::ipc

