/**
 * @file unix_socket.cpp
 * @brief Unix Domain Socket 通信层四态状态机与收发循环实现
 */

#include "unix_socket.hpp"
#include "common/log.hpp"
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <fcntl.h>
#include <poll.h>
#include <cstring>
#include <cerrno>

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
 *
 * ⚠️ EAGAIN 语义：服务端为客户端 socket 设置了 SO_SNDTIMEO(50ms)，因此阻塞 socket 上的
 * EAGAIN 表示「本次 send 超时」而非「稍后重试」。此处必须走 poll(POLLOUT) 有界等待，
 * 绝不可 continue 自旋 —— 调用链上游是 FramePublisher 线程与持 Engine::control 锁的
 * broadcast_ai_message，无界自旋会把「对端不读」升级为「整个引擎控制面挂死」。
 *
 * 调用链：write_all ← write_msg_with_fd ← UnixSocketServer::send_msg_with_fd
 *         ← FrameBroker::publish_frame / FrameBroker::broadcast_message
 *         ← Engine::broadcast_ai_message（持 Engine::control 锁）。
 *
 * @param timeout_ms 单次 poll 等待上限；每次 EAGAIN 给一次有界 poll 机会（与
 *                   write_msg_with_fd 的既有行为一致），超时或 poll 失败即返回 false。
 * @return true 全部字节已写出；false 超时、对端关闭或出错。
 */
bool write_all(int fd, const void* data, size_t size, int timeout_ms = 50) {
    const uint8_t* ptr = static_cast<const uint8_t*>(data);
    size_t remaining = size;
    while (remaining > 0) {
        ssize_t n = send(fd, ptr, remaining, MSG_NOSIGNAL);
        if (n > 0) {
            ptr += n;
            remaining -= static_cast<size_t>(n);
            continue;
        }
        if (n < 0 && errno == EINTR) continue;
        if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            struct pollfd pfd{};
            pfd.fd = fd;
            pfd.events = POLLOUT;
            const int pret = poll(&pfd, 1, timeout_ms);
            if (pret > 0 && (pfd.revents & POLLOUT)) continue;
            return false; // 超时 / POLLERR / POLLHUP / POLLNVAL
        }
        return false; // 对端关闭或不可恢复错误
    }
    return true;
}

/**
 * @brief 结构化报文写函数（可选携带 SCM_RIGHTS 文件描述符）
 */
bool write_msg_with_fd(int fd, const IpcHeader& header, const void* payload, size_t payload_size, int fd_to_send) {
    struct msghdr msg{};
    struct iovec iov[2];
    iov[0].iov_base = const_cast<void*>(static_cast<const void*>(&header));
    iov[0].iov_len = sizeof(header);
    int iovcnt = 1;
    if (payload && payload_size > 0) {
        iov[1].iov_base = const_cast<void*>(payload);
        iov[1].iov_len = payload_size;
        iovcnt = 2;
    }
    msg.msg_iov = iov;
    msg.msg_iovlen = iovcnt;

    union {
        struct cmsghdr cm;
        char control[CMSG_SPACE(sizeof(int))];
    } control_un;

    if (fd_to_send >= 0) {
        std::memset(control_un.control, 0, sizeof(control_un.control));
        msg.msg_control = control_un.control;
        msg.msg_controllen = sizeof(control_un.control);
        struct cmsghdr* cmsg = CMSG_FIRSTHDR(&msg);
        cmsg->cmsg_level = SOL_SOCKET;
        cmsg->cmsg_type = SCM_RIGHTS;
        cmsg->cmsg_len = CMSG_LEN(sizeof(int));
        std::memcpy(CMSG_DATA(cmsg), &fd_to_send, sizeof(int));
    }

    size_t total_size = sizeof(header) + payload_size;
    ssize_t n = 0;
    while (true) {
        n = sendmsg(fd, &msg, MSG_NOSIGNAL);
        if (n < 0) {
            if (errno == EINTR) continue;
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                struct pollfd pfd{};
                pfd.fd = fd;
                pfd.events = POLLOUT;
                int pret = poll(&pfd, 1, 50);
                if (pret > 0 && (pfd.revents & POLLOUT)) continue;
            }
            return false;
        }
        break;
    }

    if (static_cast<size_t>(n) == total_size) {
        return true;
    }

    // 处理局部写入：描述符已随首字节递交，剩余数据通过普通流写补齐
    size_t written = static_cast<size_t>(n);
    if (written < sizeof(header)) {
        const uint8_t* hdr_rem = reinterpret_cast<const uint8_t*>(&header) + written;
        if (!write_all(fd, hdr_rem, sizeof(header) - written)) {
            return false;
        }
        if (payload && payload_size > 0) {
            return write_all(fd, payload, payload_size);
        }
    } else {
        size_t payload_written = written - sizeof(header);
        const uint8_t* pay_rem = static_cast<const uint8_t*>(payload) + payload_written;
        return write_all(fd, pay_rem, payload_size - payload_written);
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
    } catch (const std::exception&) {
        ZL_LOGE(0, "ipc.unix_socket", "worker_exception");
    } catch (...) {
        ZL_LOGE(0, "ipc.unix_socket", "worker_unknown_exception");
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

bool UnixSocketServer::send_msg_with_fd(uint32_t client_id, uint16_t msg_type, const void* payload, size_t payload_size, int fd_to_send) {
    // ⚠️ 发送侧 payload 门禁：与接收侧（client_read_loop）对称，杜绝
    //    static_cast<uint32_t>(payload_size) 对超范围 size_t 的静默截断。
    if (payload_size > MAX_IPC_PAYLOAD_SIZE) {
        return false;
    }
    if (payload_size > 0 && payload == nullptr) {
        return false;
    }

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

    return write_msg_with_fd(cfd, header, payload, payload_size, fd_to_send);
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
    // ⚠️ 新连接必须重置挂断标记：disconnect() 只关 fd，不碰 peer_closed_（它由接收路径
    //    写入），因此这里显式复位，否则同一对象重连后 is_connected() 会永远为假。
    peer_closed_.store(false);

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

bool UnixSocketClient::send_msg_with_fd(uint16_t msg_type, const void* payload, size_t payload_size, int fd_to_send) {
    // ⚠️ 发送侧 payload 门禁：与接收侧（recv_msg_with_fd）对称，杜绝
    //    static_cast<uint32_t>(payload_size) 对超范围 size_t 的静默截断。
    if (payload_size > MAX_IPC_PAYLOAD_SIZE) {
        return false;
    }
    if (payload_size > 0 && payload == nullptr) {
        return false;
    }

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

    return write_msg_with_fd(sock, header, payload, payload_size, fd_to_send);
}

bool UnixSocketClient::recv_msg_with_fd(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int& out_fd, int timeout_ms) {
    out_fd = -1;
    int sock = fd_.load();
    if (sock < 0) return false;
    std::lock_guard<std::mutex> lock(recv_mutex_);

    sock = fd_.load();
    if (sock < 0) return false;

    if (timeout_ms >= 0) {
        struct pollfd pfd{};
        pfd.fd = sock;
        pfd.events = POLLIN;
        int ret = poll(&pfd, 1, timeout_ms);
        if (ret == 0) return false;
        if (ret < 0) {
            // poll 失败（非 EINTR）同样是不可恢复的连接错误，必须标脏，
            // 否则上层 `continue` 会退化成忙等。
            if (errno != EINTR) mark_peer_closed();
            return false;
        }
        if (pfd.revents & (POLLERR | POLLNVAL)) {
            mark_peer_closed();
            return false;
        }
        // ⚠️ 宿主进程被 kill -9 / OOM / 崩溃时，内核只会置 POLLHUP（含 POLLIN 仍可能置位），
        //    这里必须无条件标脏：只读走 recv 会拿到 EOF，但上层的 is_connected() 若仍为真，
        //    runner 主循环就会 `continue` 成 100% CPU 忙等孤儿进程。
        if (pfd.revents & POLLHUP) {
            mark_peer_closed();
            return false;
        }
    }

    struct msghdr msg{};
    struct iovec iov{};
    iov.iov_base = &out_header;
    iov.iov_len = sizeof(out_header);
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;

    union {
        struct cmsghdr cm;
        char control[CMSG_SPACE(sizeof(int)) * 2];
    } control_un;
    std::memset(control_un.control, 0, sizeof(control_un.control));
    msg.msg_control = control_un.control;
    msg.msg_controllen = sizeof(control_un.control);

    ssize_t n = 0;
    while (true) {
        n = recvmsg(sock, &msg, 0);
        if (n < 0 && (errno == EINTR)) continue;
        break;
    }
    if (n <= 0) {
        // n == 0 是对端正常关闭（EOF）；n < 0 且非 EINTR 是硬错误。两者都意味着连接已不可用。
        mark_peer_closed();
        return false;
    }

    for (struct cmsghdr* cmsg = CMSG_FIRSTHDR(&msg); cmsg != nullptr; cmsg = CMSG_NXTHDR(&msg, cmsg)) {
        if (cmsg->cmsg_level == SOL_SOCKET && cmsg->cmsg_type == SCM_RIGHTS) {
            size_t num_fds = (cmsg->cmsg_len >= CMSG_LEN(sizeof(int))) ?
                ((cmsg->cmsg_len - CMSG_LEN(0)) / sizeof(int)) : 0;
            const int* fds = reinterpret_cast<const int*>(CMSG_DATA(cmsg));
            for (size_t i = 0; i < num_fds; ++i) {
                int received_fd = fds[i];
                if (received_fd >= 0) {
                    if (out_fd < 0) {
                        int flags = fcntl(received_fd, F_GETFD);
                        if (flags != -1) {
                            fcntl(received_fd, F_SETFD, flags | FD_CLOEXEC);
                        }
                        out_fd = received_fd;
                    } else {
                        // 已经提取过主描述符，多余的描述符立即关闭，杜绝句柄泄漏
                        ::close(received_fd);
                    }
                }
            }
        }
    }

    if (static_cast<size_t>(n) < sizeof(out_header)) {
        uint8_t* ptr = reinterpret_cast<uint8_t*>(&out_header) + n;
        size_t rem = sizeof(out_header) - n;
        ReadStatus st = read_all(sock, ptr, rem, 1000);
        if (st != ReadStatus::SUCCESS) {
            if (out_fd >= 0) { ::close(out_fd); out_fd = -1; }
            // 半包脱节/EOF/超时均不可恢复，防止上层忙等。
            mark_peer_closed();
            return false;
        }
    }

    if (out_header.magic != IPC_MAGIC || out_header.payload_size > MAX_IPC_PAYLOAD_SIZE) {
        if (out_fd >= 0) { ::close(out_fd); out_fd = -1; }
        // 协议失步是本端/对端流的契约破裂，重试不可能自愈。
        mark_peer_closed();
        return false;
    }

    out_payload.resize(out_header.payload_size);
    if (out_header.payload_size > 0) {
        ReadStatus pst = read_all(sock, out_payload.data(), out_header.payload_size, 1000);
        if (pst != ReadStatus::SUCCESS) {
            if (out_fd >= 0) { ::close(out_fd); out_fd = -1; }
            mark_peer_closed();
            return false;
        }
    }
    return true;
}

bool UnixSocketClient::recv_msg(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int timeout_ms) {
    int received_fd = -1;
    bool ok = recv_msg_with_fd(out_header, out_payload, received_fd, timeout_ms);
    if (received_fd >= 0) {
        ::close(received_fd);
    }
    return ok;
}

} // namespace zhulong::ipc

