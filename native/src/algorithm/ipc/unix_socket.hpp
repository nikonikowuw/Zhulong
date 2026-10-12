#pragma once

#include "ipc_protocol.hpp"
#include <string>
#include <vector>
#include <functional>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <atomic>
#include <thread>

namespace zhulong::ipc {

struct ClientChannel {
    std::atomic<int> fd{-1};
    std::mutex send_mutex;
};

class UnixSocketServer {
public:
    using MessageCallback = std::function<void(uint32_t client_id, const IpcHeader& header, const uint8_t* payload)>;
    using ConnectCallback = std::function<void(uint32_t client_id)>;
    using DisconnectCallback = std::function<void(uint32_t client_id)>;

    UnixSocketServer();
    ~UnixSocketServer();

    bool start(const std::string& socket_path, std::string* error_msg = nullptr);
    void stop();

    void set_on_message(MessageCallback cb) { on_message_ = std::move(cb); }
    void set_on_connect(ConnectCallback cb) { on_connect_ = std::move(cb); }
    void set_on_disconnect(DisconnectCallback cb) { on_disconnect_ = std::move(cb); }

    bool send_msg(uint32_t client_id, uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0);
    size_t broadcast(uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0);

    [[nodiscard]] size_t client_count() const;
    [[nodiscard]] bool is_running() const noexcept { return running_.load(); }
    [[nodiscard]] const std::string& path() const noexcept { return socket_path_; }

private:
    void listen_loop();
    void client_read_loop(uint32_t client_id, std::shared_ptr<ClientChannel> channel);
    void remove_client(uint32_t client_id);

    std::string socket_path_;
    std::atomic<int> listen_fd_{-1};
    std::atomic<bool> running_{false};
    std::atomic<uint32_t> next_client_id_{1};
    std::atomic<size_t> active_workers_{0};

    mutable std::mutex clients_mutex_;
    std::unordered_map<uint32_t, std::shared_ptr<ClientChannel>> client_channels_;
    std::thread accept_thread_;

    MessageCallback on_message_;
    ConnectCallback on_connect_;
    DisconnectCallback on_disconnect_;
};

class UnixSocketClient {
public:
    UnixSocketClient();
    ~UnixSocketClient();

    bool connect(const std::string& socket_path, std::string* error_msg = nullptr);
    void disconnect();

    bool send_msg(uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0);
    bool recv_msg(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int timeout_ms = -1);

    [[nodiscard]] bool is_connected() const noexcept { return fd_.load() >= 0; }

private:
    std::atomic<int> fd_{-1};
    std::string socket_path_;
    std::mutex send_mutex_;
    std::mutex recv_mutex_;
};

} // namespace zhulong::ipc
