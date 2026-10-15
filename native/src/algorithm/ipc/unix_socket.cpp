/**
 * @file unix_socket.cpp
 * @brief Unix Domain Socket 通信层四态状态机与收发循环实现
 */

#include "unix_socket.hpp"
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <fcntl.h>
#include <poll.h>
#include <cstring>
#include <cerrno>
#include <iostream>

namespace zhulong::ipc {

namespace {

/**
 * @brief 四态流读取状态机
 *
 * 用于区分可重试的空闲轮询超时与不可恢复的半包截断错误：
 * - SUCCESS: 完整读取期望的所有字节；
 * - IDLE_TIMEOUT: 尚读入 0 字节时的 poll 超时，允许上层安全重试；
 * - CLOSED: 尚读入 0 字节时收到对端 EOF，正常安全断开；
 * - DESYNC_ERROR: ⚠️ 已读入部分字节后发生超时、对端关闭或错误，流已脱节，必须立即熔断关闭。
 */
enum class ReadStatus {
    SUCCESS,        // 完整读入请求的所有字节
    IDLE_TIMEOUT,   // 在读取 0 字节时发生 poll 超时 (安全空闲等待)
    CLOSED,         // 在读取 0 字节时对端正常关闭 (EOF, recv == 0)
    DESYNC_ERROR    // 已读部分字节后发生超时、对端关闭或错误 (流脱节，不可恢复)
};

/**
 * @brief 阻塞发送所有指定字节，忽略 SIGPIPE 信号
 */
bool write_all(int fd, const void* data, size_t size) {
    const uint8_t* ptr = static_cast<const uint8_t*>(data);
    size_t remaining = size;
    while (remaining > 0) {
        ssize_t n = send(fd, ptr, remaining, MSG_NOSIGNAL);
        if (n <= 0) {
            if (n < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK)) {
                continue;
            }
            return false;
        }
        ptr += n;
        remaining -= n;
    }
    return true;
}

/**
 * @brief 带超时与半包流脱节检测的定长流读取函数
 */
ReadStatus read_all(int fd, void* data, size_t size, int timeout_ms) {
    uint8_t* ptr = static_cast<uint8_t*>(data);
    size_t remaining = size;
    size_t bytes_read = 0;

    while (remaining > 0) {
        if (timeout_ms >= 0) {
            struct pollfd pfd{};
            pfd.fd = fd;
            pfd.events = POLLIN;
            int ret = poll(&pfd, 1, timeout_ms);
            if (ret == 0) {
                // 超时：若尚未读取任何字节，则为空闲超时；否则为半包流脱节
                return (bytes_read == 0) ? ReadStatus::IDLE_TIMEOUT : ReadStatus::DESYNC_ERROR;
            }
            if (ret < 0) {
                if (errno == EINTR) continue;
                return (bytes_read == 0) ? ReadStatus::CLOSED : ReadStatus::DESYNC_ERROR;
            }
            if (pfd.revents & (POLLERR | POLLNVAL)) {
                return ReadStatus::DESYNC_ERROR;
            }
        }

        ssize_t n = recv(fd, ptr, remaining, 0);
        if (n == 0) {
            // 对端关闭 (EOF)
            return (bytes_read == 0) ? ReadStatus::CLOSED : ReadStatus::DESYNC_ERROR;
        }
        if (n < 0) {
            if (errno == EINTR) continue;
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                if (timeout_ms < 0) continue;
                return (bytes_read == 0) ? ReadStatus::IDLE_TIMEOUT : ReadStatus::DESYNC_ERROR;
            }
            return ReadStatus::DESYNC_ERROR;
        }

        ptr += n;
        remaining -= n;
        bytes_read += static_cast<size_t>(n);
    }
    return ReadStatus::SUCCESS;
}

} // namespace

// =============================================================================
// UnixSocketServer Implementation
// =============================================================================

UnixSocketServer::UnixSocketServer() = default;

UnixSocketServer::~UnixSocketServer() {
    stop();
}

bool UnixSocketServer::start(const std::string& socket_path, std::string* error_msg) {
    stop();
    socket_path_ = socket_path;

    ::unlink(socket_path_.c_str());

    int fd = ::socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) {
        if (error_msg) *error_msg = std::string("socket() failed: ") + std::strerror(errno);
        return false;
    }

    struct sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    std::strncpy(addr.sun_path, socket_path_.c_str(), sizeof(addr.sun_path) - 1);

    if (::bind(fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) != 0) {
        if (error_msg) *error_msg = std::string("bind() failed: ") + std::strerror(errno);
        ::close(fd);
        return false;
    }

    if (::listen(fd, 16) != 0) {
        if (error_msg) *error_msg = std::string("listen() failed: ") + std::strerror(errno);
        ::close(fd);
        return false;
    }

    listen_fd_.store(fd);
    running_.store(true);
    accept_thread_ = std::thread(&UnixSocketServer::listen_loop, this);
    return true;
}

void UnixSocketServer::stop() {
    if (!running_.exchange(false)) {
        return;
    }

    int lfd = listen_fd_.exchange(-1);
    if (lfd >= 0) {
        ::shutdown(lfd, SHUT_RDWR);
        ::close(lfd);
    }

    if (accept_thread_.joinable()) {
        accept_thread_.join();
    }

    // 唤醒所有客户端读取线程：仅发送 shutdown，不提前 close，防止与工作线程的 recv() 产生竞态
    std::vector<std::shared_ptr<ClientChannel>> to_close;
    {
        std::lock_guard<std::mutex> lock(clients_mutex_);
        for (auto& [cid, ch] : client_channels_) {
            int cfd = ch->fd.load();
            if (cfd >= 0) {
                ::shutdown(cfd, SHUT_RDWR);
            }
            to_close.push_back(ch);
        }
        client_channels_.clear();
    }

    // 等待所有读取工作线程完全退出
    while (active_workers_.load() > 0) {
        std::this_thread::sleep_for(std::chrono::milliseconds(2));
    }

    // 此时所有工作线程均已安全退出，安全关闭剩余套接字
    for (auto& ch : to_close) {
        std::lock_guard<std::mutex> send_lock(ch->send_mutex);
        int old_fd = ch->fd.exchange(-1);
        if (old_fd >= 0) {
            ::close(old_fd);
        }
    }

    if (!socket_path_.empty()) {
        ::unlink(socket_path_.c_str());
    }
}

void UnixSocketServer::listen_loop() {
    while (running_.load()) {
        int lfd = listen_fd_.load();
        if (lfd < 0) break;

        struct pollfd pfd{};
        pfd.fd = lfd;
        pfd.events = POLLIN;

        int ret = poll(&pfd, 1, 100);
        if (ret <= 0 || !running_.load()) {
            continue;
        }

        int client_fd = ::accept(lfd, nullptr, nullptr);
        if (client_fd < 0) {
            continue;
        }

        // 配置发送超时 (50ms)，防止慢/死锁 Worker 拖死主媒体推帧线程
        struct timeval tv{};
        tv.tv_sec = 0;
        tv.tv_usec = 50000;
        setsockopt(client_fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));

        uint32_t cid = next_client_id_.fetch_add(1);
        auto channel = std::make_shared<ClientChannel>();
        channel->fd = client_fd;

        {
            std::lock_guard<std::mutex> lock(clients_mutex_);
            client_channels_[cid] = channel;
        }

        if (on_connect_) {
            on_connect_(cid);
        }

        std::thread(&UnixSocketServer::client_read_loop, this, cid, channel).detach();
    }
}

void UnixSocketServer::client_read_loop(uint32_t client_id, std::shared_ptr<ClientChannel> channel) {
    active_workers_.fetch_add(1);

    // ⚠️ R1 异常防爆门：本线程为 detach 的后台线程，未捕获的异常会直接
    // 触发 std::terminate() 终止整个进程。因此必须捕获全部异常（含未知异常），
    // 仅记录诊断后断开该客户端，绝不允许异常逃出线程入口。
    try {
        while (running_.load()) {
            int cfd = channel->fd.load();
            if (cfd < 0) break;

            IpcHeader header{};
            ReadStatus st = read_all(cfd, &header, sizeof(header), 100);
            if (st == ReadStatus::IDLE_TIMEOUT) {
                continue;
            }
            if (st != ReadStatus::SUCCESS) {
                // CLOSED 或 DESYNC_ERROR，断开清理连接
                break;
            }

            if (header.magic != IPC_MAGIC) {
                break;
            }

            if (header.payload_size > MAX_IPC_PAYLOAD_SIZE) {
                break; // 拒绝异常畸变超大包
            }

            std::vector<uint8_t> payload(header.payload_size);
            if (header.payload_size > 0) {
                ReadStatus pst = read_all(cfd, payload.data(), header.payload_size, 1000);
                if (pst != ReadStatus::SUCCESS) {
                    break;
                }
            }

            if (on_message_) {
                on_message_(client_id, header, payload.empty() ? nullptr : payload.data());
            }
        }
    } catch (const std::exception& e) {
        std::cerr << "[UnixSocketServer] Worker exception for client " << client_id << ": " << e.what() << std::endl;
    } catch (...) {
        std::cerr << "[UnixSocketServer] Unknown exception for client " << client_id << std::endl;
    }

    remove_client(client_id);
    active_workers_.fetch_sub(1);
}

void UnixSocketServer::remove_client(uint32_t client_id) {
    std::shared_ptr<ClientChannel> channel;
    {
        std::lock_guard<std::mutex> lock(clients_mutex_);
        auto it = client_channels_.find(client_id);
        if (it != client_channels_.end()) {
            channel = it->second;
            client_channels_.erase(it);
        }
    }

    if (channel) {
        {
            std::lock_guard<std::mutex> send_lock(channel->send_mutex);
            int old_fd = channel->fd.exchange(-1);
            if (old_fd >= 0) {
                ::shutdown(old_fd, SHUT_RDWR);
                ::close(old_fd);
            }
        }

        if (on_disconnect_) {
            on_disconnect_(client_id);
        }
    }
}

bool UnixSocketServer::send_msg(uint32_t client_id, uint16_t msg_type, const void* payload, size_t payload_size) {
    std::shared_ptr<ClientChannel> channel;
    {
        std::lock_guard<std::mutex> lock(clients_mutex_);
        auto it = client_channels_.find(client_id);
        if (it != client_channels_.end()) {
            channel = it->second;
        }
    }
    if (!channel) return false;

    std::lock_guard<std::mutex> send_lock(channel->send_mutex);
    int cfd = channel->fd.load();
    if (cfd < 0) return false;

    IpcHeader header{};
    header.magic = IPC_MAGIC;
    header.msg_type = msg_type;
    header.version = IPC_VERSION;
    header.payload_size = static_cast<uint32_t>(payload_size);

    if (!write_all(cfd, &header, sizeof(header))) {
        return false;
    }
    if (payload && payload_size > 0) {
        return write_all(cfd, payload, payload_size);
    }
    return true;
}

size_t UnixSocketServer::broadcast(uint16_t msg_type, const void* payload, size_t payload_size) {
    std::vector<uint32_t> ids;
    {
        std::lock_guard<std::mutex> lock(clients_mutex_);
        ids.reserve(client_channels_.size());
        for (const auto& [cid, _] : client_channels_) {
            ids.push_back(cid);
        }
    }

    size_t count = 0;
    for (uint32_t cid : ids) {
        if (send_msg(cid, msg_type, payload, payload_size)) {
            count++;
        }
    }
    return count;
}

size_t UnixSocketServer::client_count() const {
    std::lock_guard<std::mutex> lock(clients_mutex_);
    return client_channels_.size();
}

// =============================================================================
// UnixSocketClient Implementation
// =============================================================================

UnixSocketClient::UnixSocketClient() = default;

UnixSocketClient::~UnixSocketClient() {
    disconnect();
}

bool UnixSocketClient::connect(const std::string& socket_path, std::string* error_msg) {
    disconnect();
    socket_path_ = socket_path;

    int sock = ::socket(AF_UNIX, SOCK_STREAM, 0);
    if (sock < 0) {
        if (error_msg) *error_msg = std::string("socket() failed: ") + std::strerror(errno);
        return false;
    }

    struct sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    std::strncpy(addr.sun_path, socket_path_.c_str(), sizeof(addr.sun_path) - 1);

    if (::connect(sock, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) != 0) {
        if (error_msg) *error_msg = std::string("connect() failed: ") + std::strerror(errno);
        ::close(sock);
        return false;
    }

    fd_.store(sock);
    return true;
}

void UnixSocketClient::disconnect() {
    int sock = fd_.exchange(-1);
    if (sock >= 0) {
        ::shutdown(sock, SHUT_RDWR);
        ::close(sock);
    }
}

bool UnixSocketClient::send_msg(uint16_t msg_type, const void* payload, size_t payload_size) {
    int sock = fd_.load();
    if (sock < 0) return false;
    std::lock_guard<std::mutex> lock(send_mutex_);

    // 重新检查 fd
    sock = fd_.load();
    if (sock < 0) return false;

    IpcHeader header{};
    header.magic = IPC_MAGIC;
    header.msg_type = msg_type;
    header.version = IPC_VERSION;
    header.payload_size = static_cast<uint32_t>(payload_size);

    if (!write_all(sock, &header, sizeof(header))) {
        return false;
    }
    if (payload && payload_size > 0) {
        return write_all(sock, payload, payload_size);
    }
    return true;
}

bool UnixSocketClient::recv_msg(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int timeout_ms) {
    int sock = fd_.load();
    if (sock < 0) return false;
    std::lock_guard<std::mutex> lock(recv_mutex_);

    sock = fd_.load();
    if (sock < 0) return false;

    ReadStatus st = read_all(sock, &out_header, sizeof(out_header), timeout_ms);
    if (st != ReadStatus::SUCCESS) {
        return false;
    }
    if (out_header.magic != IPC_MAGIC || out_header.payload_size > MAX_IPC_PAYLOAD_SIZE) {
        return false;
    }

    out_payload.resize(out_header.payload_size);
    if (out_header.payload_size > 0) {
        if (read_all(sock, out_payload.data(), out_header.payload_size, timeout_ms) != ReadStatus::SUCCESS) {
            return false;
        }
    }
    return true;
}

} // namespace zhulong::ipc

