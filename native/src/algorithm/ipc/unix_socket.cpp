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

bool read_all(int fd, void* data, size_t size, int timeout_ms) {
    uint8_t* ptr = static_cast<uint8_t*>(data);
    size_t remaining = size;

    while (remaining > 0) {
        if (timeout_ms >= 0) {
            struct pollfd pfd{};
            pfd.fd = fd;
            pfd.events = POLLIN;
            int ret = poll(&pfd, 1, timeout_ms);
            if (ret <= 0) {
                return false;
            }
        }

        ssize_t n = recv(fd, ptr, remaining, 0);
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

    // 唤醒并关闭所有客户端
    {
        std::lock_guard<std::mutex> lock(clients_mutex_);
        for (auto& [cid, ch] : client_channels_) {
            std::lock_guard<std::mutex> send_lock(ch->send_mutex);
            if (ch->fd >= 0) {
                ::shutdown(ch->fd, SHUT_RDWR);
                ::close(ch->fd);
                ch->fd = -1;
            }
        }
        client_channels_.clear();
    }

    // 等待所有读取工作线程完全退出
    while (active_workers_.load() > 0) {
        std::this_thread::sleep_for(std::chrono::milliseconds(2));
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

    while (running_.load()) {
        int cfd = channel->fd;
        if (cfd < 0) break;

        IpcHeader header{};
        if (!read_all(cfd, &header, sizeof(header), 100)) {
            if (!running_.load()) break;
            struct pollfd pfd{};
            pfd.fd = cfd;
            pfd.events = POLLIN | POLLHUP | POLLERR;
            if (poll(&pfd, 1, 0) > 0 && (pfd.revents & (POLLHUP | POLLERR))) {
                break;
            }
            continue;
        }

        if (header.magic != IPC_MAGIC) {
            break;
        }

        std::vector<uint8_t> payload(header.payload_size);
        if (header.payload_size > 0) {
            if (!read_all(cfd, payload.data(), header.payload_size, 1000)) {
                break;
            }
        }

        if (on_message_) {
            on_message_(client_id, header, payload.empty() ? nullptr : payload.data());
        }
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
            if (channel->fd >= 0) {
                ::shutdown(channel->fd, SHUT_RDWR);
                ::close(channel->fd);
                channel->fd = -1;
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
    if (channel->fd < 0) return false;

    IpcHeader header{};
    header.magic = IPC_MAGIC;
    header.msg_type = msg_type;
    header.version = IPC_VERSION;
    header.payload_size = static_cast<uint32_t>(payload_size);

    if (!write_all(channel->fd, &header, sizeof(header))) {
        return false;
    }
    if (payload && payload_size > 0) {
        return write_all(channel->fd, payload, payload_size);
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

    if (!read_all(sock, &out_header, sizeof(out_header), timeout_ms)) {
        return false;
    }
    if (out_header.magic != IPC_MAGIC) {
        return false;
    }

    out_payload.resize(out_header.payload_size);
    if (out_header.payload_size > 0) {
        if (!read_all(sock, out_payload.data(), out_header.payload_size, timeout_ms)) {
            return false;
        }
    }
    return true;
}

} // namespace zhulong::ipc
