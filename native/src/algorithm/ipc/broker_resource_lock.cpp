/**
 * @file broker_resource_lock.cpp
 * @brief broker 资源目录锁实现（与 internal/algorunner/resource_lock_linux.go 对齐）
 */

#include "broker_resource_lock.hpp"

#include <sys/file.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <fcntl.h>
#include <unistd.h>
#include <cerrno>
#include <cstring>
#if defined(__linux__)
#include <sys/sysmacros.h>
#endif

#include <algorithm>
#include <utility>

namespace zhulong::ipc {

namespace {

/**
 * @brief 目录身份：设备号 + inode
 *
 * 与 Go 侧 fileID 完全一致（major/minor 拆位 + inode）。按它去重与排序，
 * 才能保证两侧锁的是同一批对象、且加锁顺序相同。
 */
struct DirIdentity {
    unsigned int major{0};
    unsigned int minor{0};
    ino_t inode{0};

    bool operator<(const DirIdentity& other) const {
        if (major != other.major) return major < other.major;
        if (minor != other.minor) return minor < other.minor;
        return inode < other.inode;
    }
    bool operator==(const DirIdentity& other) const {
        return major == other.major && minor == other.minor && inode == other.inode;
    }
};

struct ResolvedDir {
    DirIdentity id;
    std::string path;
};

} // namespace

std::unique_ptr<BrokerResourceLock> BrokerResourceLock::acquire(const std::vector<std::string>& dirs,
                                                               std::string* error_msg) {
    std::vector<ResolvedDir> resolved;
    resolved.reserve(dirs.size());

    for (const auto& dir : dirs) {
        if (dir.empty()) continue;
        struct stat st{};
        if (::stat(dir.c_str(), &st) != 0) {
            if (error_msg) *error_msg = "stat " + dir + " failed: " + std::strerror(errno);
            return nullptr;
        }
        if (!S_ISDIR(st.st_mode)) {
            if (error_msg) *error_msg = dir + " is not a directory";
            return nullptr;
        }
        DirIdentity id{static_cast<unsigned int>(major(st.st_dev)),
                       static_cast<unsigned int>(minor(st.st_dev)),
                       st.st_ino};
        bool seen = false;
        for (const auto& existing : resolved) {
            if (existing.id == id) { seen = true; break; }
        }
        if (!seen) {
            resolved.push_back(ResolvedDir{id, dir});
        }
    }

    // 固定加锁顺序：集合无序会导致两个进程交叉加锁而死锁。
    std::sort(resolved.begin(), resolved.end(),
              [](const ResolvedDir& a, const ResolvedDir& b) { return a.id < b.id; });

    std::unique_ptr<BrokerResourceLock> lock(new BrokerResourceLock());
    for (const auto& entry : resolved) {
        int fd = ::open(entry.path.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
        if (fd < 0) {
            if (error_msg) *error_msg = "open " + entry.path + " failed: " + std::strerror(errno);
            return nullptr; // lock 析构释放已取得的锁
        }
        // 创建侧阻塞取锁：创建过程不能跳过同步，否则会与清扫器竞态。
        while (::flock(fd, LOCK_EX) != 0) {
            if (errno == EINTR) continue;
            if (error_msg) *error_msg = "flock " + entry.path + " failed: " + std::strerror(errno);
            ::close(fd);
            return nullptr;
        }
        lock->fds_.push_back(fd);
    }
    return lock;
}

BrokerResourceLock::~BrokerResourceLock() {
    // 关闭描述符即释放 flock；显式 LOCK_UN 只为让 strace 可读。
    for (auto it = fds_.rbegin(); it != fds_.rend(); ++it) {
        ::flock(*it, LOCK_UN);
        ::close(*it);
    }
    fds_.clear();
}

} // namespace zhulong::ipc
