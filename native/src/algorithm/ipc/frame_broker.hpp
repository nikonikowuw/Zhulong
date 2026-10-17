/**
 * @file frame_broker.hpp
 * @brief 共享内存帧分发宿主（FrameBroker）契约与租约生命周期控制
 *
 * 核心设计原则：
 * 1. 租约状态机（SlotState）：FREE -> WRITING -> LEASED -> FREE 严格循环。
 * 2. 单调递增代次（generation）防 ABA：每次槽位复用分配代次自增，客户端 ACK 必须回传一致的 generation；
 *    若槽位已因超时被强制驱逐复用，过期 ACK 将被静默丢弃。
 * 3. 慢算法丢帧隔离（Drop-Latest）：每个客户端维护在途计数 in_flight_count（上限默认 2），
 *    满载时直接丢弃最新帧，绝不阻塞主视频流或其他高速算法包。
 * 4. 崩溃自愈：检测到客户端 socket 断开或 POLLHUP 时立即回收其在所有槽位上的在途租约。
 * 5. 在途配额原子回滚：publish_frame 在共享内存写入异常或发送失败时，原子回退已预分配的配额并记录 dropped_frames。
 */

#pragma once

#include "ipc_protocol.hpp"
#include "shm_pool.hpp"
#include "unix_socket.hpp"
#include <platform/dmabuf/dmabuf_allocator.hpp>
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

/**
 * @brief 共享内存槽位生命周期状态
 */
enum class SlotState {
    FREE,    /**< 槽位空闲，未被分配或前序租约已全量归还 */
    WRITING, /**< 宿主正在向该槽位写入最新解码帧数据（独占写） */
    LEASED   /**< 帧数据写入完成并已下发给客户端，处于并发只读租赁中 */
};

/**
 * @brief 单个共享内存槽位的租约跟踪器
 */
struct SlotTracker {
    uint32_t slot_index{0};                              /**< 槽位物理索引（0 ~ slot_count-1） */
    uint64_t generation{0};                              /**< 槽位代次（每次重新分配单调自增，防 ABA 悬挂确认） */
    SlotState state{SlotState::FREE};                    /**< 当前槽位状态机阶段 */
    std::unordered_set<uint32_t> active_leases;          /**< 当前借用该槽位的活跃客户端 ID 集合 */
    std::chrono::steady_clock::time_point dispatch_time; /**< 帧分发时间戳，用于超时驱逐判定 */
};

/**
 * @brief 客户端运行状态与背压统计快照
 */
struct ClientInfo {
    uint32_t client_id{0};       /**< 客户端全局唯一 ID */
    std::string name;            /**< 客户端标识名称 */
    uint32_t in_flight_count{0}; /**< 当前在途未归还租约数（<= max_in_flight_per_client） */
    uint64_t dropped_frames{0};  /**< 因背压或发送失败累计对该客户端丢弃的帧数 */
    uint64_t delivered_frames{0};/**< 成功向该客户端分发并已被消费的帧数 */
};

/**
 * @brief FrameBroker 启动配置参数
 */
struct BrokerOptions {
    size_t slot_count{DEFAULT_SLOT_COUNT};               /**< 共享内存槽位总数（默认 8） */
    size_t slot_size{DEFAULT_SLOT_SIZE};                 /**< 单槽位字节数（默认 4MB） */
    uint32_t max_in_flight_per_client{2};                /**< 单个客户端最大在途帧配额（默认 2 帧，防止慢算法堆积） */
    std::chrono::milliseconds lease_timeout{500};        /**< 租约超时阈值（默认 500ms，超时将强制驱逐） */
};

/**
 * @brief 帧发布与共享内存租赁管理宿主（单物理流对应一个实例）
 */
class FrameBroker {
public:
    FrameBroker();
    ~FrameBroker();

    // 禁用拷贝与移动
    FrameBroker(const FrameBroker&) = delete;
    FrameBroker& operator=(const FrameBroker&) = delete;

    /**
     * @brief 初始化共享内存段与 Unix 域套接字服务端并开始监听
     *
     * @param stream_id 物理流唯一 ID
     * @param base_dir 套接字存放基目录（默认 /tmp）
     * @param options 槽位与背压选项
     * @param[out] error_msg 失败时输出错误原因（可选）
     * @return true 启动成功；false 失败
     */
    bool start(uint64_t stream_id, const std::string& base_dir = "/tmp",
               BrokerOptions options = {}, std::string* error_msg = nullptr);

    /**
     * @brief 优雅停机：通知各客户端断开、释放共享内存段并销毁套接字
     */
    void stop();

    /**
     * @brief 写入帧数据到共享内存槽位并向所有已连接的 Runner 广播分发
     * @return true 若帧成功写入并至少分发或进入槽位；false 若全槽位枯竭且无法驱逐导致丢帧
     */
    bool publish_frame(const HardwareFrame& frame);

    /**
     * @brief 检查 Broker 是否处于运行状态
     */
    [[nodiscard]] bool is_running() const noexcept { return running_.load(); }

    /**
     * @brief 检查当前槽位池是否由物理连续 DMA-BUF 支撑（硬件零拷贝）
     */
    [[nodiscard]] bool has_dma_slots() const noexcept { return has_dma_slots_; }

    /**
     * @brief 获取当前在线的客户端数量
     */
    [[nodiscard]] size_t client_count() const;

    /**
     * @brief 获取累计成功发布的帧总数
     */
    [[nodiscard]] uint64_t total_published() const noexcept { return total_published_.load(); }

    /**
     * @brief 获取因槽位耗尽或写入失败累计全局丢弃的帧总数
     */
    [[nodiscard]] uint64_t total_dropped() const noexcept { return total_dropped_.load(); }

    /**
     * @brief 获取共享内存段名称
     */
    [[nodiscard]] const std::string& shm_name() const noexcept { return shm_name_; }

    /**
     * @brief 获取监听的套接字绝对路径
     */
    [[nodiscard]] const std::string& socket_path() const noexcept { return socket_path_; }

    /**
     * @brief 查询指定客户端的在途计数与统计信息
     */
    bool get_client_info(uint32_t client_id, ClientInfo& out_info) const;

    using ResultCallback = std::function<void(uint64_t stream_id, uint32_t client_id, const uint8_t* payload, size_t size)>;

    /**
     * @brief 设置收到 Runner 推理结果载荷时的回调
     */
    void set_on_result(ResultCallback cb) {
        on_result_ = std::move(cb);
    }

    /**
     * @brief 向所有已连接的 Runner 客户端广播控制消息（如底库同步/增量消息）
     * @param msg_type 消息类型枚举（如 MSG_GALLERY_SYNC / MSG_GALLERY_UPSERT）
     * @param payload 负载指针
     * @param size 负载字节大小（<= MAX_IPC_PAYLOAD_SIZE）
     * @return 成功写入发送缓冲的客户端总数
     */
    size_t broadcast_message(uint16_t msg_type, const void* payload = nullptr, size_t size = 0);

private:
    void handle_client_message(uint32_t client_id, const IpcHeader& header, const uint8_t* payload);
    void handle_client_disconnect(uint32_t client_id);
    void handle_handshake(uint32_t client_id, const HandshakeReq& req);
    void handle_ack(uint32_t client_id, const FrameAckMsg& ack);

    int find_or_evict_free_slot(std::unique_lock<std::mutex>& lock);

    /**
     * @brief 发布失败的统一回滚：归还在途配额、释放槽位、记账，返回 false。
     *
     * 内部自行加 mutex_（publish_frame 的写入段刻意在锁外执行，避免长 memcpy 持锁）。
     *
     * @param slot_idx 已分配的槽位索引（< 0 表示未分配，跳过槽位处理）
     * @param target_clients 已预分配配额的客户端列表
     * @return 恒为 false（供调用方直接 return）
     */
    bool fail_publish(int slot_idx, const std::vector<uint32_t>& target_clients);

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
    bool has_dma_slots_{false};
    std::vector<std::unique_ptr<zhulong::platform::DmaBuffer>> dma_slots_;

    mutable std::mutex mutex_;
    std::vector<SlotTracker> slots_;
    std::unordered_map<uint32_t, ClientInfo> clients_;
    ResultCallback on_result_;
};

} // namespace zhulong::ipc

