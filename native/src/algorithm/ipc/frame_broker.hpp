#pragma once

#include "ipc_protocol.hpp"
#include "shm_pool.hpp"
#include "unix_socket.hpp"
#include <Zhulong/frame.hpp>
#include <functional>
#include <string>
#include <vector>
#include <unordered_map>
#include <unordered_set>
#include <mutex>
#include <atomic>
#include <chrono>

namespace zhulong::ipc {

enum class SlotState {
    FREE,
    WRITING,
    LEASED
};

struct SlotTracker {
    uint32_t slot_index{0};
    uint64_t generation{0};
    SlotState state{SlotState::FREE};
    std::unordered_set<uint32_t> active_leases;
    std::chrono::steady_clock::time_point dispatch_time;
};

struct ClientInfo {
    uint32_t client_id{0};
    std::string name;
    uint32_t in_flight_count{0};
    uint64_t dropped_frames{0};
    uint64_t delivered_frames{0};
};

struct BrokerOptions {
    size_t slot_count{DEFAULT_SLOT_COUNT};
    size_t slot_size{DEFAULT_SLOT_SIZE};
    uint32_t max_in_flight_per_client{2};
    std::chrono::milliseconds lease_timeout{500};
};

class FrameBroker {
public:
    FrameBroker();
    ~FrameBroker();

    // 禁用拷贝与移动
    FrameBroker(const FrameBroker&) = delete;
    FrameBroker& operator=(const FrameBroker&) = delete;

    bool start(uint64_t stream_id, const std::string& base_dir = "/tmp",
               BrokerOptions options = {}, std::string* error_msg = nullptr);
    void stop();

    /**
     * @brief 写入帧数据到共享内存槽位并向所有已连接的 Runner 广播分发
     * @return true 若帧成功写入并至少分发或进入槽位；false 若全槽位枯竭且无法驱逐导致丢帧
     */
    bool publish_frame(const HardwareFrame& frame);

    [[nodiscard]] bool is_running() const noexcept { return running_.load(); }
    [[nodiscard]] size_t client_count() const;
    [[nodiscard]] uint64_t total_published() const noexcept { return total_published_.load(); }
    [[nodiscard]] uint64_t total_dropped() const noexcept { return total_dropped_.load(); }
    [[nodiscard]] const std::string& shm_name() const noexcept { return shm_name_; }
    [[nodiscard]] const std::string& socket_path() const noexcept { return socket_path_; }

    // 获取客户端统计信息
    bool get_client_info(uint32_t client_id, ClientInfo& out_info) const;

    using ResultCallback = std::function<void(uint64_t stream_id, uint32_t client_id, const uint8_t* payload, size_t size)>;
    void set_on_result(ResultCallback cb) {
        on_result_ = std::move(cb);
    }

private:
    void handle_client_message(uint32_t client_id, const IpcHeader& header, const uint8_t* payload);
    void handle_client_disconnect(uint32_t client_id);
    void handle_handshake(uint32_t client_id, const HandshakeReq& req);
    void handle_ack(uint32_t client_id, const FrameAckMsg& ack);

    int find_or_evict_free_slot(std::unique_lock<std::mutex>& lock);

    uint64_t stream_id_{0};
    std::string shm_name_;
    std::string socket_path_;
    BrokerOptions options_;

    std::atomic<bool> running_{false};
    std::atomic<uint64_t> frame_seq_{1};
    std::atomic<uint64_t> total_published_{0};
    std::atomic<uint64_t> total_dropped_{0};

    ShmPool shm_pool_;
    UnixSocketServer server_;

    mutable std::mutex mutex_;
    std::vector<SlotTracker> slots_;
    std::unordered_map<uint32_t, ClientInfo> clients_;
    ResultCallback on_result_;
};

} // namespace zhulong::ipc
